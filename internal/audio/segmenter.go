package audio

import (
	"context"
	"time"

	"github.com/pid1/voxthief/internal/events"
)

// Params configures the squelch state machine (§5). Zero values are not
// meaningful defaults; callers populate these from config.AudioConfig.
type Params struct {
	ThresholdDBFS float64 // RMS gate per 20 ms block
	OpenBlocks    int     // consecutive above-threshold blocks to OPEN
	HangTimeS     float64 // continuous below-threshold time to CLOSE
	PrerollMS     int     // ring buffer prepended on OPEN
	MaxSegmentS   float64 // hard cap; force-close with Capped=true
	MinSegmentS   float64 // shorter segments discarded pre-transcription
}

// levelEvery throttles LevelMsg emission to ~10 Hz: one every 5 blocks of
// 20 ms = every 100 ms (§5).
const levelEvery = 5

// Segmenter is the squelch state machine (§5). It consumes real-time-paced
// 20 ms blocks and emits assembled Segments on OPEN→CLOSE transitions. Timing
// is derived from block counts (each block is exactly BlockDuration) rather
// than time.Now, so behavior is deterministic and testable.
type Segmenter struct {
	p    Params
	emit func(any) // optional; surfaces Level/SquelchOpened/SquelchClosed events

	prerollBlocks int
	hangBlocks    int // blocks of continuous silence to close
	maxBlocks     int // accumulated blocks that force a capped close
	minBlocks     int // active blocks below which a segment is discarded
}

// NewSegmenter builds a Segmenter from p. emit may be nil; when set it receives
// events.LevelMsg, events.SquelchOpenedMsg and events.SquelchClosedMsg values.
func NewSegmenter(p Params, emit func(any)) *Segmenter {
	s := &Segmenter{p: p, emit: emit}
	s.prerollBlocks = ceilBlocks(float64(p.PrerollMS) / float64(BlockMillis))
	s.hangBlocks = ceilBlocks(p.HangTimeS * 1000 / float64(BlockMillis))
	s.maxBlocks = ceilBlocks(p.MaxSegmentS * 1000 / float64(BlockMillis))
	s.minBlocks = ceilBlocks(p.MinSegmentS * 1000 / float64(BlockMillis))
	if s.hangBlocks < 1 {
		s.hangBlocks = 1
	}
	return s
}

// ceilBlocks rounds a fractional block count up to the next whole block, with a
// floor of zero.
func ceilBlocks(f float64) int {
	if f <= 0 {
		return 0
	}
	n := int(f)
	if float64(n) < f {
		n++
	}
	return n
}

// Run starts one goroutine consuming in and emitting completed Segments on the
// returned channel. The out channel is closed when in closes or ctx is done;
// any open segment is flushed before shutdown (§3.2, §5).
func (s *Segmenter) Run(ctx context.Context, in <-chan Block) <-chan Segment {
	out := make(chan Segment)
	go s.loop(ctx, in, out)
	return out
}

// segmenter state, kept in loop locals below. Named here for reference:
//   preroll: ring buffer of the most recent pre-speech blocks (up to
//     prerollBlocks). Snapshotted on OPEN and prepended to the segment.
//   pending: the current run of consecutive above-threshold blocks while
//     CLOSED; promoted into the segment when it reaches OpenBlocks.
//   acc:     blocks accumulated since OPEN (excludes preroll).

func (s *Segmenter) loop(ctx context.Context, in <-chan Block, out chan<- Segment) {
	defer close(out)

	var (
		open       bool
		preroll    []Block // ring buffer (trimmed to prerollBlocks)
		pending    []Block // current above-threshold run while closed
		acc        []Block // blocks since OPEN
		startedAt  time.Time
		silenceRun int // consecutive below-threshold blocks while open
		lastActive int // count of acc blocks through the last above-threshold one
		blockN     int // global block counter for level throttling
	)

	for {
		select {
		case <-ctx.Done():
			if open {
				s.flush(out, preroll, acc, startedAt, lastActive, false)
			}
			return
		case b, ok := <-in:
			if !ok {
				if open {
					s.flush(out, preroll, acc, startedAt, lastActive, false)
				}
				return
			}

			rms := RMSDBFS(b.Samples)
			above := rms >= s.p.ThresholdDBFS

			if blockN%levelEvery == 0 {
				s.emitLevel(rms, b.Samples)
			}
			blockN++

			if !open {
				if above {
					pending = append(pending, b)
					if len(pending) >= s.p.OpenBlocks {
						// OPEN: the triggering run becomes the segment head;
						// StartedAt is the first speech block's wall clock (§5).
						open = true
						acc = pending
						pending = nil
						startedAt = acc[0].At
						silenceRun = 0
						lastActive = len(acc)
						s.emitMsg(events.SquelchOpenedMsg{At: startedAt})
					}
				} else {
					// Run broke below OpenBlocks: those blocks and this one
					// become preroll context.
					for _, pb := range pending {
						preroll = pushRing(preroll, pb, s.prerollBlocks)
					}
					pending = nil
					preroll = pushRing(preroll, b, s.prerollBlocks)
				}
				continue
			}

			// OPEN: accumulate every block (internal gaps within hang time are
			// part of the transmission).
			acc = append(acc, b)
			if above {
				silenceRun = 0
				lastActive = len(acc)
			} else {
				silenceRun++
			}

			if len(acc) >= s.maxBlocks {
				// Force-close at max_segment_s (stuck-squelch guard, §5).
				s.flush(out, preroll, acc, startedAt, len(acc), true)
				open, acc, preroll, pending = false, nil, preroll[:0], nil
				continue
			}
			if silenceRun >= s.hangBlocks {
				// Continuous sub-threshold audio exceeded hang_time_s.
				s.flush(out, preroll, acc, startedAt, lastActive, false)
				open, acc, preroll, pending = false, nil, preroll[:0], nil
				continue
			}
		}
	}
}

// flush assembles preroll+acc into a Segment, emits SquelchClosedMsg on every
// close (including capped), and sends the segment downstream unless it is
// shorter than min_segment_s (§5). activeBlocks measures the transmission span
// from OPEN through the last above-threshold block; capped segments report
// their full accumulated length.
func (s *Segmenter) flush(out chan<- Segment, preroll, acc []Block, startedAt time.Time, activeBlocks int, capped bool) {
	dur := time.Duration(activeBlocks) * BlockDuration
	s.emitMsg(events.SquelchClosedMsg{Duration: dur, Capped: capped})

	if activeBlocks < s.minBlocks {
		return // discard sub-minimum transmissions, do not emit downstream
	}

	seg := Segment{
		Samples:   assemble(preroll, acc),
		StartedAt: startedAt,
		Duration:  dur,
		Capped:    capped,
	}
	// Blocking send: on shutdown the transcriber keeps draining until the
	// channel closes (§3.2 shutdown order), so the final segment is delivered.
	out <- seg
}

// assemble flattens preroll followed by acc into one contiguous sample slice.
func assemble(preroll, acc []Block) []float32 {
	n := 0
	for _, b := range preroll {
		n += len(b.Samples)
	}
	for _, b := range acc {
		n += len(b.Samples)
	}
	out := make([]float32, 0, n)
	for _, b := range preroll {
		out = append(out, b.Samples...)
	}
	for _, b := range acc {
		out = append(out, b.Samples...)
	}
	return out
}

// pushRing appends b to a ring buffer trimmed to the most recent capacity
// blocks. capacity 0 yields an empty preroll.
func pushRing(ring []Block, b Block, capacity int) []Block {
	if capacity <= 0 {
		return ring[:0]
	}
	ring = append(ring, b)
	if len(ring) > capacity {
		ring = ring[len(ring)-capacity:]
	}
	return ring
}

func (s *Segmenter) emitMsg(msg any) {
	if s.emit != nil {
		s.emit(msg)
	}
}

func (s *Segmenter) emitLevel(rms float64, samples []float32) {
	if s.emit == nil {
		return
	}
	s.emit(events.LevelMsg{RMSDBFS: rms, PeakDBFS: PeakDBFS(samples)})
}
