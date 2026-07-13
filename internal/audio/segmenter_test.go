package audio

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pid1/voxthief/internal/events"
)

var segBase = time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)

// testParams: small, whole-block timings for deterministic assertions.
//
//	open_blocks 3, hang 5 blocks, preroll 2 blocks, max 20 blocks, min 5 blocks.
//
// The max cap is set high enough that only TestSegmenterMaxCap trips it; the
// other cases exercise hang-close and preroll without the stuck-squelch guard.
func testParams() Params {
	return Params{
		ThresholdDBFS: -45,
		OpenBlocks:    3,
		HangTimeS:     0.1, // 5 blocks
		PrerollMS:     40,  // 2 blocks
		MaxSegmentS:   0.4, // 20 blocks
		MinSegmentS:   0.1, // 5 blocks
	}
}

// blockRun appends count constant-amplitude blocks starting at index idx.
func blockRun(blocks []Block, idx, count int, amp float32) []Block {
	for i := 0; i < count; i++ {
		s := make([]float32, SamplesPerBlock)
		for j := range s {
			s[j] = amp
		}
		blocks = append(blocks, Block{Samples: s, At: segBase.Add(time.Duration(idx+i) * BlockDuration)})
	}
	return blocks
}

const (
	loudAmp = 0.3   // ~-10 dBFS, above -45 threshold
	subAmp  = 0.001 // ~-60 dBFS, below threshold (used for preroll)
)

// runSeg feeds blocks through the segmenter and collects emitted segments and
// events after the pipeline drains.
func runSeg(t *testing.T, p Params, blocks []Block) ([]Segment, []any) {
	t.Helper()
	var mu sync.Mutex
	var msgs []any
	emit := func(m any) {
		mu.Lock()
		msgs = append(msgs, m)
		mu.Unlock()
	}

	s := NewSegmenter(p, emit)
	in := make(chan Block)
	out := s.Run(context.Background(), in)
	go func() {
		for _, b := range blocks {
			in <- b
		}
		close(in)
	}()

	var segs []Segment
	for seg := range out {
		segs = append(segs, seg)
	}
	return segs, msgs
}

func countMsgs[T any](msgs []any) int {
	n := 0
	for _, m := range msgs {
		if _, ok := m.(T); ok {
			n++
		}
	}
	return n
}

func TestSegmenterBasicOpenClose(t *testing.T) {
	t.Parallel()
	p := testParams()

	var blocks []Block
	blocks = blockRun(blocks, 0, 2, subAmp)  // preroll (below threshold)
	blocks = blockRun(blocks, 2, 6, loudAmp) // speech (opens after 3)
	blocks = blockRun(blocks, 8, 5, subAmp)  // hang -> close

	segs, msgs := runSeg(t, p, blocks)
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1", len(segs))
	}
	seg := segs[0]

	// StartedAt is the first speech block (index 2).
	wantStart := segBase.Add(2 * BlockDuration)
	if !seg.StartedAt.Equal(wantStart) {
		t.Errorf("StartedAt = %v, want %v", seg.StartedAt, wantStart)
	}
	if seg.Capped {
		t.Errorf("Capped = true, want false")
	}
	// Active span = 6 speech blocks.
	if want := 6 * BlockDuration; seg.Duration != want {
		t.Errorf("Duration = %v, want %v", seg.Duration, want)
	}
	// Samples = 2 preroll + 6 speech + 5 hang blocks.
	if want := (2 + 6 + 5) * SamplesPerBlock; len(seg.Samples) != want {
		t.Errorf("len(Samples) = %d, want %d", len(seg.Samples), want)
	}
	// Preroll content is prepended: first sample is the sub-threshold value.
	if seg.Samples[0] != subAmp {
		t.Errorf("Samples[0] = %v, want preroll %v", seg.Samples[0], subAmp)
	}
	// Speech content present after preroll.
	if seg.Samples[2*SamplesPerBlock] != loudAmp {
		t.Errorf("Samples[preroll end] = %v, want speech %v", seg.Samples[2*SamplesPerBlock], loudAmp)
	}

	if got := countMsgs[events.SquelchOpenedMsg](msgs); got != 1 {
		t.Errorf("SquelchOpenedMsg count = %d, want 1", got)
	}
	if got := countMsgs[events.SquelchClosedMsg](msgs); got != 1 {
		t.Errorf("SquelchClosedMsg count = %d, want 1", got)
	}
	if got := countMsgs[events.LevelMsg](msgs); got == 0 {
		t.Errorf("no LevelMsg emitted")
	}
}

func TestSegmenterPrerollRingTrim(t *testing.T) {
	t.Parallel()
	p := testParams()

	var blocks []Block
	blocks = blockRun(blocks, 0, 4, subAmp)  // 4 pre-speech, ring keeps last 2
	blocks = blockRun(blocks, 4, 6, loudAmp) // speech
	blocks = blockRun(blocks, 10, 5, subAmp) // hang

	segs, _ := runSeg(t, p, blocks)
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1", len(segs))
	}
	// Only 2 preroll blocks retained despite 4 sub-threshold blocks.
	if want := (2 + 6 + 5) * SamplesPerBlock; len(segs[0].Samples) != want {
		t.Errorf("len(Samples) = %d, want %d (preroll trimmed to 2)", len(segs[0].Samples), want)
	}
}

func TestSegmenterHangHoldsSegment(t *testing.T) {
	t.Parallel()
	p := testParams()

	// Two speech bursts separated by silence shorter than hang time stay in
	// one segment.
	var blocks []Block
	blocks = blockRun(blocks, 0, 5, loudAmp) // open + speech
	blocks = blockRun(blocks, 5, 3, subAmp)  // gap < hang (5)
	blocks = blockRun(blocks, 8, 3, loudAmp) // more speech
	blocks = blockRun(blocks, 11, 5, subAmp) // hang -> close

	segs, _ := runSeg(t, p, blocks)
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1 (gap under hang time)", len(segs))
	}
	// Active span runs through the last speech block: 5 + 3 + 3 = 11 blocks.
	if want := 11 * BlockDuration; segs[0].Duration != want {
		t.Errorf("Duration = %v, want %v", segs[0].Duration, want)
	}
}

func TestSegmenterHangSplitsSegments(t *testing.T) {
	t.Parallel()
	p := testParams()

	var blocks []Block
	blocks = blockRun(blocks, 0, 6, loudAmp)  // segment 1
	blocks = blockRun(blocks, 6, 5, subAmp)   // hang closes 1
	blocks = blockRun(blocks, 11, 6, loudAmp) // segment 2
	blocks = blockRun(blocks, 17, 5, subAmp)  // hang closes 2

	segs, msgs := runSeg(t, p, blocks)
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2 (gap exceeds hang)", len(segs))
	}
	if got := countMsgs[events.SquelchOpenedMsg](msgs); got != 2 {
		t.Errorf("SquelchOpenedMsg count = %d, want 2", got)
	}
	if got := countMsgs[events.SquelchClosedMsg](msgs); got != 2 {
		t.Errorf("SquelchClosedMsg count = %d, want 2", got)
	}
}

func TestSegmenterMaxCap(t *testing.T) {
	t.Parallel()
	p := testParams()

	var blocks []Block
	blocks = blockRun(blocks, 0, 20, loudAmp) // reaches max (20 blocks) -> cap
	blocks = blockRun(blocks, 20, 5, subAmp)  // trailing silence, no reopen

	segs, msgs := runSeg(t, p, blocks)
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1", len(segs))
	}
	if !segs[0].Capped {
		t.Errorf("Capped = false, want true (max_segment cap)")
	}
	if want := 20 * BlockDuration; segs[0].Duration != want {
		t.Errorf("Duration = %v, want %v", segs[0].Duration, want)
	}
	if got := countMsgs[events.SquelchClosedMsg](msgs); got != 1 {
		t.Errorf("SquelchClosedMsg count = %d, want 1", got)
	}
}

func TestSegmenterMinLengthDiscard(t *testing.T) {
	t.Parallel()
	p := testParams()

	// 3 speech blocks (active span 3 < min 5) then hang: closes but discarded.
	var blocks []Block
	blocks = blockRun(blocks, 0, 3, loudAmp)
	blocks = blockRun(blocks, 3, 5, subAmp)

	segs, msgs := runSeg(t, p, blocks)
	if len(segs) != 0 {
		t.Fatalf("got %d segments, want 0 (below min_segment)", len(segs))
	}
	// The squelch still opened and closed; only downstream emission is skipped.
	if got := countMsgs[events.SquelchOpenedMsg](msgs); got != 1 {
		t.Errorf("SquelchOpenedMsg count = %d, want 1", got)
	}
	if got := countMsgs[events.SquelchClosedMsg](msgs); got != 1 {
		t.Errorf("SquelchClosedMsg count = %d, want 1", got)
	}
}

func TestSegmenterFlushOnShutdown(t *testing.T) {
	t.Parallel()
	p := testParams()

	// Open with enough speech to pass min, then close the input mid-transmission
	// (no hang): the open segment must be flushed.
	var blocks []Block
	blocks = blockRun(blocks, 0, 6, loudAmp)

	segs, msgs := runSeg(t, p, blocks)
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1 (flush on shutdown)", len(segs))
	}
	if segs[0].Capped {
		t.Errorf("Capped = true, want false on flush")
	}
	if got := countMsgs[events.SquelchClosedMsg](msgs); got != 1 {
		t.Errorf("SquelchClosedMsg count = %d, want 1", got)
	}
}

func TestSegmenterContextCancelFlush(t *testing.T) {
	t.Parallel()
	p := testParams()

	var mu sync.Mutex
	var msgs []any
	emit := func(m any) { mu.Lock(); msgs = append(msgs, m); mu.Unlock() }

	s := NewSegmenter(p, emit)
	in := make(chan Block)
	ctx, cancel := context.WithCancel(context.Background())
	out := s.Run(ctx, in)

	// Feed enough speech to open and pass min, then cancel.
	blocks := blockRun(nil, 0, 6, loudAmp)
	go func() {
		for _, b := range blocks {
			in <- b
		}
		cancel() // cancel instead of closing input
	}()

	var segs []Segment
	for seg := range out {
		segs = append(segs, seg)
	}
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1 flushed on cancel", len(segs))
	}
}
