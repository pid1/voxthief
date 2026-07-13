package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// captureChanCap bounds the block channel; on overflow the oldest block is
// dropped and counted, never blocking the capture callback (§3.2).
const captureChanCap = 64

// DeviceInfo is one enumerated capture device for the `inputs` command (§4.1).
type DeviceInfo struct {
	Index    int
	Name     string
	Channels int
}

// SoundCardSource captures from an OS input device via malgo/miniaudio,
// requesting 16 kHz mono f32 and letting miniaudio convert from the device's
// native rate (§4.2). It covers both the USB-adapter and native-jack paths
// identically. A microphone always produces real-time frames, so no silence
// synthesis is needed to satisfy the source contract (§3.1).
type SoundCardSource struct {
	// Selector is a device index, a name substring, or "default"/"" (§4.2).
	Selector string

	name     string
	deviceID malgo.DeviceID
	useID    bool

	ctxDev   *malgo.AllocatedContext
	device   *malgo.Device
	out      chan Block
	residual []float32 // partial block accumulator (callback thread only)
	drops    atomic.Int64
	shutOnce sync.Once
}

// Start opens the resolved capture device and streams 20 ms blocks. Device
// resolution errors (no match / ambiguity) are returned here with the candidate
// list (§4.2).
func (s *SoundCardSource) Start(ctx context.Context) (<-chan Block, error) {
	cctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}
	s.ctxDev = cctx

	if err := s.resolve(cctx.Context); err != nil {
		_ = cctx.Uninit()
		cctx.Free()
		s.ctxDev = nil
		return nil, err
	}

	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatF32
	cfg.Capture.Channels = 1
	cfg.SampleRate = SampleRate
	cfg.Alsa.NoMMap = 1
	if s.useID {
		cfg.Capture.DeviceID = unsafe.Pointer(&s.deviceID)
	}

	s.out = make(chan Block, captureChanCap)
	callbacks := malgo.DeviceCallbacks{Data: s.onRecvFrames}
	dev, err := malgo.InitDevice(cctx.Context, cfg, callbacks)
	if err != nil {
		_ = cctx.Uninit()
		cctx.Free()
		s.ctxDev = nil
		return nil, fmt.Errorf("open capture device %q: %w", s.name, err)
	}
	s.device = dev
	if err := dev.Start(); err != nil {
		dev.Uninit()
		_ = cctx.Uninit()
		cctx.Free()
		s.ctxDev = nil
		return nil, fmt.Errorf("start capture device %q: %w", s.name, err)
	}

	// Stop and clean up when the caller cancels the context.
	go func() {
		<-ctx.Done()
		s.shutdown()
	}()
	return s.out, nil
}

// onRecvFrames runs on miniaudio's capture thread. It converts f32 frames into
// 20 ms blocks, dropping the oldest block on channel overflow (§3.2).
func (s *SoundCardSource) onRecvFrames(_, pInput []byte, frameCount uint32) {
	n := int(frameCount)
	for i := 0; i < n; i++ {
		bits := binary.LittleEndian.Uint32(pInput[i*4:])
		s.residual = append(s.residual, math.Float32frombits(bits))
	}
	for len(s.residual) >= SamplesPerBlock {
		block := make([]float32, SamplesPerBlock)
		copy(block, s.residual[:SamplesPerBlock])
		s.residual = s.residual[SamplesPerBlock:]
		s.send(Block{Samples: block, At: time.Now().UTC()})
	}
}

// send enqueues b, dropping the oldest queued block on overflow (§3.2).
func (s *SoundCardSource) send(b Block) {
	select {
	case s.out <- b:
		return
	default:
	}
	select {
	case <-s.out: // discard oldest
		s.drops.Add(1)
	default:
	}
	select {
	case s.out <- b:
	default:
		s.drops.Add(1)
	}
}

// Drops returns the running count of dropped input blocks (§3.2).
func (s *SoundCardSource) Drops() int64 { return s.drops.Load() }

// Stop halts capture and releases the device and context.
func (s *SoundCardSource) Stop() error {
	s.shutdown()
	return nil
}

// shutdown stops the device (ending callbacks) before closing the channel and
// freeing the context; idempotent.
func (s *SoundCardSource) shutdown() {
	s.shutOnce.Do(func() {
		if s.device != nil {
			s.device.Uninit() // blocks until callbacks quiesce
		}
		if s.ctxDev != nil {
			_ = s.ctxDev.Uninit()
			s.ctxDev.Free()
		}
		if s.out != nil {
			close(s.out)
		}
	})
}

// Descriptor returns "soundcard:<name>" (§4.2), persisted to the DB.
func (s *SoundCardSource) Descriptor() string {
	return "soundcard:" + s.name
}

// FrequencyHz is always nil for a soundcard source (§3.1).
func (s *SoundCardSource) FrequencyHz() *int64 { return nil }

// resolve maps the selector to a concrete device, per §4.2: index | name
// substring | "default". A no-match or ambiguous substring is an error listing
// the candidate device names.
func (s *SoundCardSource) resolve(ctx malgo.Context) error {
	sel := strings.TrimSpace(s.Selector)
	devices, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return fmt.Errorf("enumerate capture devices: %w", err)
	}

	if sel == "" || strings.EqualFold(sel, "default") {
		s.useID = false
		s.name = defaultName(devices)
		return nil
	}

	// Numeric index.
	if idx, err := strconv.Atoi(sel); err == nil {
		if idx < 0 || idx >= len(devices) {
			return fmt.Errorf("soundcard device index %d out of range: %s", idx, candidateList(devices))
		}
		s.selectDevice(devices[idx])
		return nil
	}

	// Case-insensitive name substring.
	var matches []malgo.DeviceInfo
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Name()), strings.ToLower(sel)) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return fmt.Errorf("no capture device matches %q: %s", sel, candidateList(devices))
	case 1:
		s.selectDevice(matches[0])
		return nil
	default:
		return fmt.Errorf("selector %q matches %d capture devices, be more specific: %s", sel, len(matches), candidateList(matches))
	}
}

func (s *SoundCardSource) selectDevice(d malgo.DeviceInfo) {
	s.deviceID = d.ID
	s.useID = true
	s.name = d.Name()
}

func defaultName(devices []malgo.DeviceInfo) string {
	for _, d := range devices {
		if d.IsDefault != 0 {
			return d.Name()
		}
	}
	return "default"
}

func candidateList(devices []malgo.DeviceInfo) string {
	if len(devices) == 0 {
		return "no capture devices found"
	}
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = fmt.Sprintf("%d:%q", i, d.Name())
	}
	return "candidates: " + strings.Join(names, ", ")
}

// EnumerateCaptureDevices lists capture devices for the `inputs` command (§4.1).
func EnumerateCaptureDevices() ([]DeviceInfo, error) {
	cctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}
	defer func() {
		_ = cctx.Uninit()
		cctx.Free()
	}()

	devices, err := cctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("enumerate capture devices: %w", err)
	}
	out := make([]DeviceInfo, 0, len(devices))
	for i, d := range devices {
		ch := 0
		// The basic listing omits format detail; query full info for channels.
		if full, ferr := cctx.DeviceInfo(malgo.Capture, d.ID, malgo.Shared); ferr == nil {
			for _, f := range full.Formats {
				if int(f.Channels) > ch {
					ch = int(f.Channels)
				}
			}
		}
		out = append(out, DeviceInfo{Index: i, Name: d.Name(), Channels: ch})
	}
	return out, nil
}
