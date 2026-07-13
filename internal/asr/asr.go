// Package asr wraps whisper.cpp for local speech-to-text with integrated
// Silero VAD, and provides the app-side hallucination filters and the
// first-run model downloader (§6).
//
// The real transcriber is a cgo wrapper (whisper.go, built only under
// `-tags whisper`). The default build compiles the stub (stub.go) so the rest
// of the module builds and tests without a C toolchain. Everything in this
// file is tag-neutral and shared by both builds.
package asr

import "runtime"

// Params configures a single transcription session (§6). Paths point at GGML
// model files resolved by the downloader (models.go).
type Params struct {
	ModelPath     string // path to the whisper GGML model (e.g. ggml-small.en-q8_0.bin)
	VADModelPath  string // path to the Silero VAD GGML model (ggml-silero-v6.2.0.bin)
	BeamSize      int    // beam search width; whisper beam_size
	Language      string // e.g. "en"
	InitialPrompt string // optional conditioning prompt
	NoContext     bool   // §6: true — do not carry context across windows
	Threads       int    // whisper n_threads
	VAD           bool   // enable whisper.cpp integrated VAD
}

// Segment is one whisper output segment. Start/End are seconds relative to the
// input audio, always mapped to the ORIGINAL untrimmed timeline even when VAD
// trims silence internally (§6 — the VAD timeline-mapping correctness rule).
type Segment struct {
	Start        float64 // seconds from the start of the untrimmed input
	End          float64 // seconds from the start of the untrimmed input
	Text         string
	AvgLogprob   float64 // mean ln(p) over the segment's tokens
	NoSpeechProb float64 // whisper no_speech probability for the segment
}

// Result is the full transcription of one audio segment.
type Result struct {
	Segments []Segment
	Language string
}

// Transcriber transcribes 16 kHz mono f32 PCM. Implementations are not safe
// for concurrent use; the pipeline runs one worker per transcriber by default
// (§3.2). Close releases the underlying whisper context.
type Transcriber interface {
	Transcribe(samples []float32) (Result, error)
	Close() error
}

// DefaultThreads returns the whisper thread count per §6: min(NumCPU, 8).
func DefaultThreads() int {
	return min(runtime.NumCPU(), 8)
}

// NullTranscriber returns no segments. It stands in when the binary is built
// without whisper (Available == false): capture and segmentation still run and
// rows are recorded (as filtered), but there is no transcription.
type NullTranscriber struct{}

func (NullTranscriber) Transcribe([]float32) (Result, error) { return Result{}, nil }
func (NullTranscriber) Close() error                         { return nil }

// New is declared per build tag: the real cgo constructor in whisper.go
// (//go:build whisper) and the error-returning stub in stub.go
// (//go:build !whisper). It is intentionally not declared here.
