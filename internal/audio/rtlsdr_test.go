package audio

import (
	"context"
	"encoding/binary"
	"os/exec"
	"testing"
	"time"
)

func TestParseFrequency(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want int64
		bad  bool
	}{
		{"146.520M", 146520000, false},
		{"462.7125M", 462712500, false},
		{"162.550M", 162550000, false},
		{"462712500", 462712500, false},
		{"1090M", 1090000000, false},
		{"144000k", 144000000, false},
		{"1.2G", 1200000000, false},
		{"  146.520M ", 146520000, false},
		{"", 0, true},
		{"abc", 0, true},
		{"-5M", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseFrequency(tt.in)
		if tt.bad {
			if err == nil {
				t.Errorf("ParseFrequency(%q) = %d, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseFrequency(%q) error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseFrequency(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// fakeRTLFMCmd emits s16le audio, then stalls (simulating a closed hardware
// squelch) while keeping the process alive — exercising the silence-synthesis
// contract (§4.3). Because a subprocess that exits on its own is an unexpected
// exit (§4.3), the fake never exits; the test ends it with Stop().
func fakeRTLFMCmd() func() *exec.Cmd {
	// Emit ~2 blocks of tone (1280 bytes = 640*2), then hold the process open so
	// the reader must synthesize silence at wall-clock cadence.
	script := `head -c 1280 /dev/zero | tr '\0' '\001'; sleep 5`
	return func() *exec.Cmd {
		return exec.Command("sh", "-c", script)
	}
}

func TestRTLSDRSilenceSynthesisAndContinuity(t *testing.T) {
	src := NewRTLSDRSource(146520000, 0, "auto", 0, 50)
	src.CmdFactory = fakeRTLFMCmd()
	src.tickInterval = 5 * time.Millisecond // keep the suite fast

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := src.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Collect blocks until we have seen audible audio followed by synthesized
	// silence (continuity across the stall), then stop the source.
	var silent, audible int
	timeout := time.After(2 * time.Second)
	for silent == 0 || audible == 0 {
		select {
		case b, ok := <-ch:
			if !ok {
				t.Fatal("channel closed unexpectedly before Stop")
			}
			if len(b.Samples) != SamplesPerBlock {
				t.Fatalf("block has %d samples, want %d", len(b.Samples), SamplesPerBlock)
			}
			if isSilent(b.Samples) {
				silent++
			} else {
				audible++
			}
		case <-timeout:
			t.Fatalf("timed out (audible=%d silent=%d)", audible, silent)
		}
	}

	if err := src.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Drain remaining blocks until the channel closes.
	for range ch {
	}
	// A requested Stop is clean, not fatal.
	select {
	case ferr := <-src.Fatal():
		t.Errorf("Stop should be clean, got fatal: %v", ferr)
	default:
	}
}

func TestRTLSDRPrematureExitIsFatal(t *testing.T) {
	src := NewRTLSDRSource(146520000, 0, "auto", 0, 50)
	// A command that exits non-zero immediately with no output.
	src.CmdFactory = func() *exec.Cmd { return exec.Command("sh", "-c", "exit 3") }
	src.tickInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := src.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Drain the channel until it closes.
	drained := make(chan struct{})
	go func() {
		for range ch {
		}
		close(drained)
	}()

	select {
	case ferr := <-src.Fatal():
		if ferr == nil {
			t.Fatal("expected non-nil fatal error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected fatal error on premature exit, got none")
	}
	<-drained
}

func TestRTLSDRStopIsCleanNotFatal(t *testing.T) {
	src := NewRTLSDRSource(146520000, 0, "auto", 0, 50)
	// A long-running command that would only stop on signal.
	src.CmdFactory = func() *exec.Cmd {
		return exec.Command("sh", "-c", "while true; do head -c 640 /dev/zero | tr '\\0' '\\001'; sleep 0.01; done")
	}
	src.tickInterval = 5 * time.Millisecond

	ctx := context.Background()
	ch, err := src.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	go func() {
		for range ch {
		}
	}()
	time.Sleep(50 * time.Millisecond)
	if err := src.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// A requested Stop must not surface as fatal.
	select {
	case ferr := <-src.Fatal():
		t.Errorf("Stop should be clean, got fatal: %v", ferr)
	case <-time.After(100 * time.Millisecond):
	}
}

func isSilent(s []float32) bool {
	for _, v := range s {
		if v != 0 {
			return false
		}
	}
	return true
}

// sanity helper: ensure our fake byte pattern decodes to non-zero samples.
func TestBlockFromS16LENonZero(t *testing.T) {
	t.Parallel()
	b := make([]byte, bytesPerBlock)
	for i := range b {
		b[i] = 0x01
	}
	blk := blockFromS16LE(b, time.Now())
	want := float32(int16(binary.LittleEndian.Uint16([]byte{0x01, 0x01}))) / 32768
	if blk.Samples[0] != want {
		t.Fatalf("sample0 = %v, want %v", blk.Samples[0], want)
	}
}
