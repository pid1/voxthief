package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// FileSource plays a 16 kHz mono WAV file as 20 ms blocks, implementing
// AudioSource. It is the headless-integration double for the real sources
// (§14): with Paced=true it emits at real-time cadence like hardware; with
// Paced=false it emits as fast as possible and closes, for deterministic tests.
type FileSource struct {
	Path  string
	Paced bool

	cancel context.CancelFunc
}

// Start decodes the WAV up front (surfacing format errors synchronously) then
// streams blocks on the returned channel until EOF, ctx cancellation, or Stop.
func (f *FileSource) Start(ctx context.Context) (<-chan Block, error) {
	samples, err := decodeWAVMono16k(f.Path)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	f.cancel = cancel

	out := make(chan Block)
	go func() {
		defer close(out)
		start := time.Now().UTC()

		var ticker *time.Ticker
		if f.Paced {
			ticker = time.NewTicker(BlockDuration)
			defer ticker.Stop()
		}

		for i := 0; i*SamplesPerBlock < len(samples); i++ {
			lo := i * SamplesPerBlock
			hi := lo + SamplesPerBlock
			block := make([]float32, SamplesPerBlock)
			if hi > len(samples) {
				hi = len(samples) // final partial block is zero-padded
			}
			copy(block, samples[lo:hi])

			b := Block{Samples: block, At: start.Add(time.Duration(i) * BlockDuration)}
			select {
			case out <- b:
			case <-ctx.Done():
				return
			}

			if f.Paced {
				select {
				case <-ticker.C:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// Stop halts streaming.
func (f *FileSource) Stop() error {
	if f.cancel != nil {
		f.cancel()
	}
	return nil
}

// Descriptor returns "file:<basename>".
func (f *FileSource) Descriptor() string {
	return "file:" + filepath.Base(f.Path)
}

// FrequencyHz is always nil for a file source.
func (f *FileSource) FrequencyHz() *int64 { return nil }

// decodeWAVMono16k reads a canonical PCM WAV and returns float32 samples in
// [-1, 1]. It requires mono, 16 kHz, 16-bit PCM — the fixed internal format
// (§5) — and skips unknown chunks.
func decodeWAVMono16k(path string) ([]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil {
		return nil, fmt.Errorf("read RIFF header: %w", err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s: not a RIFF/WAVE file", path)
	}

	var (
		gotFmt        bool
		audioFormat   uint16
		numChannels   uint16
		sampleRate    uint32
		bitsPerSample uint16
		data          []byte
	)
	for {
		var ch [8]byte
		if _, err := io.ReadFull(f, ch[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}
		id := string(ch[0:4])
		size := binary.LittleEndian.Uint32(ch[4:8])

		switch id {
		case "fmt ":
			body := make([]byte, size)
			if _, err := io.ReadFull(f, body); err != nil {
				return nil, err
			}
			if size < 16 {
				return nil, fmt.Errorf("%s: short fmt chunk", path)
			}
			audioFormat = binary.LittleEndian.Uint16(body[0:2])
			numChannels = binary.LittleEndian.Uint16(body[2:4])
			sampleRate = binary.LittleEndian.Uint32(body[4:8])
			bitsPerSample = binary.LittleEndian.Uint16(body[14:16])
			gotFmt = true
		case "data":
			data = make([]byte, size)
			if _, err := io.ReadFull(f, data); err != nil {
				return nil, err
			}
		default:
			if _, err := f.Seek(int64(size), io.SeekCurrent); err != nil {
				return nil, err
			}
		}
		if size%2 == 1 { // chunks are word-aligned
			if _, err := f.Seek(1, io.SeekCurrent); err != nil && err != io.EOF {
				return nil, err
			}
		}
	}

	if !gotFmt {
		return nil, fmt.Errorf("%s: missing fmt chunk", path)
	}
	if audioFormat != 1 || bitsPerSample != 16 {
		return nil, fmt.Errorf("%s: require 16-bit PCM (got format %d, %d-bit)", path, audioFormat, bitsPerSample)
	}
	if numChannels != 1 {
		return nil, fmt.Errorf("%s: require mono (got %d channels)", path, numChannels)
	}
	if sampleRate != SampleRate {
		return nil, fmt.Errorf("%s: require %d Hz (got %d Hz)", path, SampleRate, sampleRate)
	}

	n := len(data) / 2
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		v := int16(binary.LittleEndian.Uint16(data[i*2:]))
		samples[i] = float32(v) / 32768
	}
	return samples, nil
}
