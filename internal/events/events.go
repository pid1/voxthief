// Package events defines the message types the capture/transcription pipeline
// sends to the UI layer (TUI via program.Send, or headless serializer). Kept
// dependency-free so any layer can consume it without import cycles (§9).
package events

import "time"

// Transmission is the display/serialization view of a finished transmission
// row. It carries exactly what the TUI and headless JSON need, decoupled from
// the DB model.
type Transmission struct {
	ID           int64
	StartedAt    time.Time
	Duration     time.Duration
	Source       string
	FrequencyHz  *int64
	Text         string
	Status       string // pending | transcribed | filtered | error
	FilterReason string
	AvgLogprob   *float64
	Capped       bool
	Alerted      bool
	AlertRules   []string
}

// LevelMsg reports per-block loudness, throttled to ~10 Hz for the meter.
type LevelMsg struct {
	RMSDBFS  float64
	PeakDBFS float64
}

// SquelchOpenedMsg is emitted when the segmenter opens on speech.
type SquelchOpenedMsg struct {
	At time.Time
}

// SquelchClosedMsg is emitted when a segment closes.
type SquelchClosedMsg struct {
	Duration time.Duration
	Capped   bool
}

// TranscribingMsg reports transcriber queue depth for the transient spinner row.
type TranscribingMsg struct {
	QueueDepth int
}

// TransmissionMsg carries a finished (transcribed/filtered/error) transmission.
type TransmissionMsg struct {
	Transmission
}

// AlertMsg reports the outcome of an alert dispatch for a transmission.
type AlertMsg struct {
	Rules  []string
	Status string // sent | failed | suppressed
	Reason string // suppress reason or error detail (secrets redacted)
}

// StatusMsg is a free-form status-bar line; Warn renders it as a warning.
type StatusMsg struct {
	Text string
	Warn bool
}

// DropMsg reports the running count of dropped input blocks (buffer overflow).
type DropMsg struct {
	Count int64
}

// ModelProgressMsg reports first-run model download progress (0..1).
type ModelProgressMsg struct {
	Name     string
	Fraction float64
	Done     bool
	Err      string
}
