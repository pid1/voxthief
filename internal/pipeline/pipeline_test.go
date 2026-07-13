package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pid1/voxthief/internal/alerts"
	"github.com/pid1/voxthief/internal/asr"
	"github.com/pid1/voxthief/internal/audio"
	"github.com/pid1/voxthief/internal/config"
	"github.com/pid1/voxthief/internal/db"
)

// fakeTranscriber returns a fixed transcript regardless of input, so the
// headless pipeline can be exercised without whisper.
type fakeTranscriber struct{ text string }

func (f fakeTranscriber) Transcribe([]float32) (asr.Result, error) {
	return asr.Result{
		Language: "en",
		Segments: []asr.Segment{{Start: 0, End: 1, Text: f.text, AvgLogprob: -0.3, NoSpeechProb: 0.1}},
	}, nil
}
func (f fakeTranscriber) Close() error { return nil }

func writeToneWAV(t *testing.T, dir string) string {
	t.Helper()
	// 1 second of a loud constant signal (~-10 dBFS), which the segmenter opens
	// on and flushes when the file source hits EOF.
	samples := make([]float32, audio.SampleRate)
	for i := range samples {
		samples[i] = 0.3
	}
	path := filepath.Join(dir, "tone.wav")
	if err := audio.WriteWAV(path, samples); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHeadlessPipelineJSONLAndDB(t *testing.T) {
	dir := t.TempDir()
	wav := writeToneWAV(t, dir)

	store, err := db.Open(filepath.Join(dir, "voxthief.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}

	rules, err := alerts.Compile([]config.RuleConfig{{Name: "callsign", Pattern: `\bK5ABC\b`}})
	if err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	emit := NewHeadlessEmitter(&out, &errw, false)

	opts := Options{
		Source: &audio.FileSource{Path: wav, Paced: false},
		Seg: audio.Params{
			ThresholdDBFS: -45, OpenBlocks: 3, HangTimeS: 1.75,
			PrerollMS: 400, MaxSegmentS: 120, MinSegmentS: 0.4,
		},
		NewTranscriber: func() (asr.Transcriber, error) {
			return fakeTranscriber{text: "K5ABC calling all units"}, nil
		},
		Workers:   1,
		Store:     store,
		FilterCfg: asr.DefaultFilterConfig(),
		Rules:     rules,
		Model:     "fake",
		Emit:      emit,
	}

	ctx := context.Background()
	if err := Run(ctx, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// One JSON object emitted on stdout with the §10 shape.
	line := bytes.TrimSpace(out.Bytes())
	if len(line) == 0 {
		t.Fatal("no headless JSON emitted")
	}
	var rec headlessRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		t.Fatalf("bad JSON %q: %v", line, err)
	}
	if rec.Status != "transcribed" {
		t.Errorf("status = %q, want transcribed", rec.Status)
	}
	if rec.Text != "K5ABC calling all units" {
		t.Errorf("text = %q", rec.Text)
	}
	if !rec.Alerted || len(rec.AlertRules) != 1 || rec.AlertRules[0] != "callsign" {
		t.Errorf("alert fields wrong: alerted=%v rules=%v", rec.Alerted, rec.AlertRules)
	}
	if rec.Source != "file:tone.wav" {
		t.Errorf("source = %q", rec.Source)
	}

	// DB row persisted as transcribed.
	rows, err := store.ListTransmissionsSince(ctx, db.FromUnix(0), db.FromUnix(1e12), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 db row, got %d", len(rows))
	}
	if rows[0].Status != "transcribed" || rows[0].Text.String != "K5ABC calling all units" {
		t.Errorf("db row = %+v", rows[0])
	}
}

func TestHeadlessPipelineFiltered(t *testing.T) {
	dir := t.TempDir()
	wav := writeToneWAV(t, dir)
	store, _ := db.Open(filepath.Join(dir, "voxthief.db"))
	defer store.Close()
	_, _ = store.Upgrade(context.Background())

	var out, errw bytes.Buffer
	opts := Options{
		Source: &audio.FileSource{Path: wav, Paced: false},
		Seg: audio.Params{
			ThresholdDBFS: -45, OpenBlocks: 3, HangTimeS: 1.75,
			PrerollMS: 400, MaxSegmentS: 120, MinSegmentS: 0.4,
		},
		NewTranscriber: func() (asr.Transcriber, error) {
			return fakeTranscriber{text: "thank you"}, nil // blocklisted → filtered
		},
		Workers:   1,
		Store:     store,
		FilterCfg: asr.DefaultFilterConfig(),
		Model:     "fake",
		Emit:      NewHeadlessEmitter(&out, &errw, false),
	}
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	var rec headlessRecord
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Status != "filtered" {
		t.Errorf("status = %q, want filtered", rec.Status)
	}
	if rec.Alerted {
		t.Errorf("filtered row must not alert")
	}
}
