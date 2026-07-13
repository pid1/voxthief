package pipeline

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pid1/voxthief/internal/asr"
	"github.com/pid1/voxthief/internal/audio"
	"github.com/pid1/voxthief/internal/db"
)

// blockingSource emits loud blocks continuously until its context is cancelled,
// signaling once the squelch has had enough blocks to open. It models a live
// source (rtl_fm/soundcard) that only stops on shutdown.
type blockingSource struct {
	opened chan struct{}
	once   sync.Once
}

func (s *blockingSource) Start(ctx context.Context) (<-chan audio.Block, error) {
	out := make(chan audio.Block)
	loud := make([]float32, audio.SamplesPerBlock)
	for i := range loud {
		loud[i] = 0.3
	}
	go func() {
		defer close(out)
		n := 0
		for {
			blk := audio.Block{Samples: append([]float32(nil), loud...), At: time.Now()}
			select {
			case <-ctx.Done():
				return
			case out <- blk:
				n++
				if n == 8 {
					s.once.Do(func() { close(s.opened) })
				}
			}
		}
	}()
	return out, nil
}

func (s *blockingSource) Stop() error         { return nil }
func (s *blockingSource) Descriptor() string  { return "test:blocking" }
func (s *blockingSource) FrequencyHz() *int64 { return nil }

// TestGracefulDrainPersistsFlushedSegment reproduces the live-hardware bug: on
// SIGINT the segmenter flushes its open segment, and the transcriber must still
// persist it. Before the fix the DB write used the cancelled capture context
// and failed with "context canceled", losing the final transmission.
func TestGracefulDrainPersistsFlushedSegment(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "voxthief.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}

	src := &blockingSource{opened: make(chan struct{})}
	opts := Options{
		Source: src,
		Seg: audio.Params{
			ThresholdDBFS: -45, OpenBlocks: 3, HangTimeS: 1.75,
			PrerollMS: 40, MaxSegmentS: 120, MinSegmentS: 0.02, // low min so a short flush persists
		},
		NewTranscriber: func() (asr.Transcriber, error) {
			return fakeTranscriber{text: "drained transmission"}, nil
		},
		Workers:   1,
		Store:     store,
		FilterCfg: asr.DefaultFilterConfig(),
		Model:     "fake",
		Emit:      func(any) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = Run(ctx, opts)
		close(done)
	}()

	<-src.opened // squelch is open, a segment is accumulating
	cancel()     // simulate SIGINT
	<-done       // Run drains and returns

	rows, err := store.ListTransmissionsSince(context.Background(), db.FromUnix(0), db.FromUnix(1e12), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("flushed segment not persisted on shutdown: got %d rows, want 1", len(rows))
	}
	if rows[0].Status != "transcribed" || rows[0].Text.String != "drained transmission" {
		t.Fatalf("flushed row not transcribed: %+v", rows[0])
	}
}
