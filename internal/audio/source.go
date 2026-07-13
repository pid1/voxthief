// Package audio defines the fixed internal audio format, the AudioSource
// contract, and the capture/segmentation pipeline. Everything downstream of a
// source is identical regardless of which source is running (§3.1).
package audio

import (
	"context"
	"time"
)

// Fixed internal format (§5): 16 kHz, mono, float32 in [-1, 1], 20 ms blocks.
const (
	SampleRate      = 16000                           // Hz
	BlockMillis     = 20                              // ms per block
	SamplesPerBlock = SampleRate * BlockMillis / 1000 // 320 samples
	BlockDuration   = BlockMillis * time.Millisecond
)

// Block is a single 20 ms chunk of mono PCM at 16 kHz.
type Block struct {
	Samples []float32 // SamplesPerBlock samples: 20 ms @ 16 kHz mono
	At      time.Time // wall clock (UTC) of block start
}

// AudioSource is the single abstraction every input implements (§3.1). No
// downstream component may branch on the concrete source type.
type AudioSource interface {
	// Start begins producing real-time-paced 20 ms blocks on the returned
	// channel, continuously while running — synthesizing zero blocks at
	// wall-clock cadence when its upstream is silent. The channel is closed
	// when the source stops or ctx is cancelled.
	Start(ctx context.Context) (<-chan Block, error)
	// Stop halts production and releases resources.
	Stop() error
	// Descriptor is persisted to the DB, e.g. "soundcard:USB Audio Device"
	// or "rtlsdr:146520000@dev0".
	Descriptor() string
	// FrequencyHz is nil for soundcard, or the tuned frequency for SDR.
	FrequencyHz() *int64
}

// Segment is a closed transmission handed from the segmenter to the
// transcriber: assembled preroll + speech PCM plus timing (§5).
type Segment struct {
	Samples   []float32     // preroll + speech, 16 kHz mono f32 in [-1,1]
	StartedAt time.Time     // wall clock (UTC) captured at OPEN
	Duration  time.Duration // wall-clock span of the segment
	Capped    bool          // force-closed at max_segment_s (stuck squelch)
}

// SilenceBlock returns a zero-filled block stamped at t. Sources synthesize
// these to satisfy the real-time-cadence contract during upstream silence.
func SilenceBlock(t time.Time) Block {
	return Block{Samples: make([]float32, SamplesPerBlock), At: t}
}
