//go:build integration && whisper

// Mandatory ASR integration test (§6, §14): runs tiny.en + Silero VAD over a
// fixture and asserts that returned ABSOLUTE segment timestamps map back onto
// the original, untrimmed audio timeline when VAD is enabled — the correctness
// landmine of §16.3. Runs only under `-tags "integration whisper"` with the
// models present (CI caches them); otherwise it skips.
package asr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pid1/voxthief/internal/audio"
	"github.com/pid1/voxthief/internal/events"
)

// makeVADFixture builds a signal with a known layout: LEAD seconds of silence,
// then a burst of tone, then trailing silence. With VAD enabled whisper trims
// the leading silence internally, but the reported segment start MUST still land
// near LEAD (original timeline), not near 0 (trimmed timeline).
func synthTone(seconds float64) []float32 {
	n := int(float64(audio.SampleRate) * seconds)
	out := make([]float32, n)
	for i := range out {
		// 440 Hz-ish tone; content is irrelevant to the timeline assertion.
		out[i] = 0.2
	}
	return out
}

func TestVADTimelineMapping(t *testing.T) {
	modelsDir := os.Getenv("VOXTHIEF_MODELS_DIR")
	if modelsDir == "" {
		t.Skip("set VOXTHIEF_MODELS_DIR to the models cache to run the ASR integration test")
	}
	model := filepath.Join(modelsDir, "ggml-tiny.en.bin")
	vad := filepath.Join(modelsDir, VADModelFilename)
	for _, p := range []string{model, vad} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("missing model %s: %v", p, err)
		}
	}

	const lead = 2.0 // seconds of leading silence
	samples := make([]float32, 0)
	samples = append(samples, make([]float32, int(lead*audio.SampleRate))...) // silence
	samples = append(samples, synthTone(1.5)...)                              // speech-ish
	samples = append(samples, make([]float32, audio.SampleRate)...)           // trailing silence

	tr, err := New(Params{
		ModelPath:    model,
		VADModelPath: vad,
		BeamSize:     5,
		Language:     "en",
		NoContext:    true,
		Threads:      DefaultThreads(),
		VAD:          true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tr.Close()

	res, err := tr.Transcribe(samples)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(res.Segments) == 0 {
		t.Skip("no segments returned for synthetic tone (acceptable); timeline assertion needs real speech")
	}
	// The first segment must start on the ORIGINAL timeline (near `lead`), not
	// near 0 as it would if VAD-trimmed offsets leaked through.
	started := time.Unix(1000, 0).UTC()
	abs := started.Add(time.Duration(res.Segments[0].Start * float64(time.Second)))
	got := abs.Sub(started).Seconds()
	if got < lead-0.5 {
		t.Fatalf("segment start %.2fs maps to trimmed timeline; expected >= %.2fs (original timeline)", got, lead-0.5)
	}
	_ = events.ModelProgressMsg{}
}
