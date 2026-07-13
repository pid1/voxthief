//go:build whisper

// Real end-to-end test: drives the full pipeline (FileSource → segmenter →
// REAL whisper.cpp transcriber → filters → store → headless JSON) over an
// actual speech WAV. Builds only with `-tags whisper` and runs only when
// VOXTHIEF_MODELS_DIR and VOXTHIEF_SPEECH_WAV are set (models are large).
package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pid1/voxthief/internal/alerts"
	"github.com/pid1/voxthief/internal/asr"
	"github.com/pid1/voxthief/internal/audio"
	"github.com/pid1/voxthief/internal/config"
	"github.com/pid1/voxthief/internal/db"
)

func TestEndToEndRealWhisper(t *testing.T) {
	modelsDir := os.Getenv("VOXTHIEF_MODELS_DIR")
	wav := os.Getenv("VOXTHIEF_SPEECH_WAV")
	if modelsDir == "" || wav == "" {
		t.Skip("set VOXTHIEF_MODELS_DIR and VOXTHIEF_SPEECH_WAV to run the real E2E test")
	}
	model := filepath.Join(modelsDir, "ggml-small.en-q8_0.bin")
	vad := filepath.Join(modelsDir, asr.VADModelFilename)

	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "voxthief.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}

	rules, _ := alerts.Compile([]config.RuleConfig{{Name: "street", Pattern: `(?i)main street`}})

	var out, errw bytes.Buffer
	opts := Options{
		Source: &audio.FileSource{Path: wav, Paced: false},
		Seg: audio.Params{
			ThresholdDBFS: -45, OpenBlocks: 3, HangTimeS: 1.75,
			PrerollMS: 400, MaxSegmentS: 120, MinSegmentS: 0.4,
		},
		NewTranscriber: func() (asr.Transcriber, error) {
			return asr.New(asr.Params{
				ModelPath: model, VADModelPath: vad, BeamSize: 5, Language: "en",
				NoContext: true, Threads: asr.DefaultThreads(), VAD: true,
			})
		},
		Workers:   1,
		Store:     store,
		FilterCfg: asr.DefaultFilterConfig(),
		Rules:     rules,
		Model:     "small.en-q8_0",
		Emit:      NewHeadlessEmitter(&out, &errw, false),
	}

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	line := bytes.TrimSpace(out.Bytes())
	if len(line) == 0 {
		t.Fatalf("no transcript emitted; stderr:\n%s", errw.String())
	}
	// There may be more than one transmission; check them all.
	var full strings.Builder
	alerted := false
	for _, l := range bytes.Split(line, []byte("\n")) {
		var rec headlessRecord
		if err := json.Unmarshal(l, &rec); err != nil {
			t.Fatalf("bad JSON %q: %v", l, err)
		}
		t.Logf("transcript: status=%s text=%q alerted=%v", rec.Status, rec.Text, rec.Alerted)
		full.WriteString(strings.ToLower(rec.Text))
		full.WriteByte(' ')
		if rec.Alerted {
			alerted = true
		}
	}
	got := full.String()
	// whisper isn't deterministic word-for-word, but these domain tokens should
	// survive on clean synthetic speech.
	for _, want := range []string{"main street", "respond"} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q; got: %q", want, got)
		}
	}
	if !alerted {
		t.Errorf("expected the 'main street' rule to fire; transcript: %q", got)
	}
}
