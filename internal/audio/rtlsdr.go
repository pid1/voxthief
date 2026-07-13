package audio

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// bytesPerBlock is the wire size of one 20 ms block: 320 s16le samples (§4.3).
const bytesPerBlock = SamplesPerBlock * 2

// RTLSDRSource captures NBFM audio from an RTL-SDR dongle by driving rtl_fm as
// a subprocess and reading raw s16le mono 16 kHz from its stdout (§4.3). To
// satisfy the source contract (§3.1) it synthesizes zero blocks at wall-clock
// cadence whenever rtl_fm produces nothing (hardware squelch closed).
type RTLSDRSource struct {
	FreqHz      int64
	DeviceIndex int
	Gain        string // "auto"/"" for auto, else dB (e.g. "28")
	PPM         int
	Squelch     int
	// WideFM selects wideband FM (broadcast stations, ~200 kHz) instead of the
	// default narrowband FM used by 2-way radio. It widens the IF bandwidth and
	// uses the fast demodulator; audio is still resampled to the fixed 16 kHz.
	WideFM bool

	// CmdFactory builds the subprocess. nil uses the real rtl_fm command; tests
	// inject a fake here (the io.Reader seam is rtl_fm's stdout pipe).
	CmdFactory func() *exec.Cmd
	// tickInterval paces block emission; defaults to BlockDuration. Tests may
	// shorten it to keep the suite fast.
	tickInterval time.Duration

	cmd      *exec.Cmd
	cancel   context.CancelFunc
	stopping atomic.Bool
	waitOnce sync.Once
	waitErr  error
	fatal    chan error
}

// NewRTLSDRSource constructs a source tuned to freqHz on dongle deviceIndex.
func NewRTLSDRSource(freqHz int64, deviceIndex int, gain string, ppm, squelch int) *RTLSDRSource {
	return &RTLSDRSource{
		FreqHz:      freqHz,
		DeviceIndex: deviceIndex,
		Gain:        gain,
		PPM:         ppm,
		Squelch:     squelch,
	}
}

// Start launches rtl_fm and begins streaming real-time-paced blocks. It
// verifies rtl_fm is installed (unless a CmdFactory is injected) and returns an
// actionable error if not (§4.3, §14).
func (s *RTLSDRSource) Start(ctx context.Context) (<-chan Block, error) {
	if s.CmdFactory == nil {
		if _, err := exec.LookPath("rtl_fm"); err != nil {
			return nil, fmt.Errorf("rtl_fm not found in PATH: %s", installHint())
		}
		s.CmdFactory = s.defaultCmd
	}
	if s.tickInterval <= 0 {
		s.tickInterval = BlockDuration
	}
	s.fatal = make(chan error, 1)

	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	cmd := s.CmdFactory()
	s.cmd = cmd
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start rtl_fm: %w", err)
	}

	go drainStderr(stderr, s.Descriptor())

	out := make(chan Block)
	go s.run(ctx, stdout, out)
	return out, nil
}

// defaultCmd builds the real rtl_fm invocation (§4.3).
func (s *RTLSDRSource) defaultCmd() *exec.Cmd {
	args := []string{
		"-d", strconv.Itoa(s.DeviceIndex),
		"-f", strconv.FormatInt(s.FreqHz, 10),
		"-M", "fm",
	}
	if s.WideFM {
		// Wideband broadcast FM: wide IF bandwidth + fast atan demodulator,
		// resampled down to the fixed 16 kHz pipeline rate.
		args = append(args, "-s", "170k", "-A", "fast")
	} else {
		args = append(args, "-s", "24k")
	}
	args = append(args, "-r", "16k", "-E", "deemp")
	if g := strings.TrimSpace(s.Gain); g != "" && g != "auto" {
		args = append(args, "-g", g)
	}
	if s.PPM != 0 {
		args = append(args, "-p", strconv.Itoa(s.PPM))
	}
	args = append(args, "-l", strconv.Itoa(s.Squelch), "-")
	return exec.Command("rtl_fm", args...)
}

// run is the reader loop: it consumes rtl_fm's stdout and emits one block per
// tickInterval, synthesizing silence when no full block of audio is available
// (§4.3). It closes out on shutdown and surfaces an unexpected exit on Fatal().
func (s *RTLSDRSource) run(ctx context.Context, stdout io.Reader, out chan<- Block) {
	defer close(out)

	// A background pump decouples the blocking stdout read from the real-time
	// tick, so a stalled (squelched) rtl_fm yields silence rather than blocking.
	dataCh := make(chan []byte, 64)
	readErr := make(chan error, 1)
	go func() {
		p := make([]byte, 8192)
		for {
			n, err := stdout.Read(p)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, p[:n])
				select {
				case dataCh <- chunk:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				readErr <- err
				close(dataCh)
				return
			}
		}
	}()

	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()

	var buf []byte
	dataClosed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for done := false; !done; {
				select {
				case c, ok := <-dataCh:
					if !ok {
						dataClosed = true
						done = true
						break
					}
					buf = append(buf, c...)
				default:
					done = true
				}
			}

			now := time.Now().UTC()
			var block Block
			if len(buf) >= bytesPerBlock {
				block = blockFromS16LE(buf[:bytesPerBlock], now)
				buf = buf[bytesPerBlock:]
			} else {
				block = SilenceBlock(now)
			}
			select {
			case out <- block:
			case <-ctx.Done():
				return
			}

			// rtl_fm's stdout is exhausted and we have flushed all full blocks:
			// the subprocess has exited. An exit we did not request is fatal.
			if dataClosed && len(buf) < bytesPerBlock {
				var rerr error
				select {
				case rerr = <-readErr:
				default:
				}
				s.handleExit(rerr)
				return
			}
		}
	}
}

// handleExit reaps the process and, unless Stop initiated the shutdown, reports
// the unexpected exit on the Fatal channel (§4.3).
func (s *RTLSDRSource) handleExit(readErr error) {
	waitErr := s.reap()
	if s.stopping.Load() {
		return // expected shutdown
	}
	msg := "rtl_fm exited unexpectedly"
	switch {
	case waitErr != nil:
		msg = fmt.Sprintf("%s: %v", msg, waitErr)
	case readErr != nil && readErr != io.EOF:
		msg = fmt.Sprintf("%s: %v", msg, readErr)
	}
	select {
	case s.fatal <- fmt.Errorf("%s", msg):
	default:
	}
}

// reap waits for the subprocess exactly once.
func (s *RTLSDRSource) reap() error {
	s.waitOnce.Do(func() {
		if s.cmd != nil {
			s.waitErr = s.cmd.Wait()
		}
	})
	return s.waitErr
}

// Fatal delivers an unexpected-exit error (§4.3). Nil until Start is called.
func (s *RTLSDRSource) Fatal() <-chan error { return s.fatal }

// Stop signals rtl_fm with SIGTERM, escalating to SIGKILL after 2 s (§4.3).
func (s *RTLSDRSource) Stop() error {
	s.stopping.Store(true)
	if s.cancel != nil {
		s.cancel()
	}
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}

	done := make(chan struct{})
	go func() {
		s.reap()
		close(done)
	}()

	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
	return nil
}

// Descriptor returns "rtlsdr:<hz>@dev<idx>" (§4.3), persisted to the DB.
func (s *RTLSDRSource) Descriptor() string {
	return fmt.Sprintf("rtlsdr:%d@dev%d", s.FreqHz, s.DeviceIndex)
}

// FrequencyHz returns the tuned frequency (§4.3).
func (s *RTLSDRSource) FrequencyHz() *int64 {
	f := s.FreqHz
	return &f
}

// blockFromS16LE converts 640 bytes of little-endian s16 into a 320-sample
// float32 block in [-1, 1) stamped at t.
func blockFromS16LE(b []byte, t time.Time) Block {
	samples := make([]float32, SamplesPerBlock)
	for i := 0; i < SamplesPerBlock; i++ {
		v := int16(binary.LittleEndian.Uint16(b[i*2:]))
		samples[i] = float32(v) / 32768
	}
	return Block{Samples: samples, At: t}
}

// drainStderr logs rtl_fm's diagnostics line by line (§4.3).
func drainStderr(r io.Reader, descriptor string) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			slog.Debug("rtl_fm", "source", descriptor, "stderr", line)
		}
	}
}

// installHint returns per-OS guidance for installing the rtl-sdr tools (§14).
func installHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "install the rtl-sdr tools with `brew install rtl-sdr`"
	case "windows":
		return "install the rtl-sdr release build and add its directory to PATH (see osmocom rtl-sdr)"
	default:
		return "install the rtl-sdr tools, e.g. `sudo apt install rtl-sdr` or `sudo dnf install rtl-sdr`"
	}
}

// ParseFrequency parses a tuning frequency accepting bare Hz integers and
// k/M/G suffixes, e.g. "146.520M" → 146520000, "462712500", "162.550M" (§4.1).
func ParseFrequency(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty frequency")
	}
	mult := 1.0
	switch last := s[len(s)-1]; last {
	case 'k', 'K':
		mult = 1e3
		s = s[:len(s)-1]
	case 'm', 'M':
		mult = 1e6
		s = s[:len(s)-1]
	case 'g', 'G':
		mult = 1e9
		s = s[:len(s)-1]
	}
	s = strings.TrimSpace(s)
	if mult == 1.0 {
		// Bare Hz: keep integer precision.
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			if v <= 0 {
				return 0, fmt.Errorf("frequency must be positive: %q", s)
			}
			return v, nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid frequency %q: %w", s, err)
	}
	hz := int64(f*mult + 0.5)
	if hz <= 0 {
		return 0, fmt.Errorf("frequency must be positive: %q", s)
	}
	return hz, nil
}
