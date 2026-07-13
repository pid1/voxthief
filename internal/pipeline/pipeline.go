// Package pipeline wires the source → segmenter → transcriber → store/alerts
// data path (§3). It has no knowledge of the concrete source type and emits UI
// events through an Emit callback (program.Send for the TUI, a JSON writer for
// headless). The transcription worker owns all DB writes (single-writer, §3.2).
package pipeline

import (
	"context"
	"log/slog"
	"time"

	"github.com/pid1/voxthief/internal/alerts"
	"github.com/pid1/voxthief/internal/asr"
	"github.com/pid1/voxthief/internal/audio"
	"github.com/pid1/voxthief/internal/db"
	"github.com/pid1/voxthief/internal/events"
)

// Options configures a pipeline run.
type Options struct {
	Source audio.AudioSource
	Seg    audio.Params

	// NewTranscriber builds a transcriber; called once per worker so instances
	// are not shared (whisper transcribers are not concurrency-safe, §3.2).
	NewTranscriber func() (asr.Transcriber, error)
	Workers        int

	Store     *db.Store
	FilterCfg asr.FilterConfig

	// Alerts (optional). Rules nil / Dispatcher nil disables alerting.
	Rules      []alerts.Rule
	Dispatcher *alerts.Dispatcher

	Model       string
	RetainAudio bool
	AudioDir    string

	Emit    func(any) // UI sink (program.Send or headless writer)
	Verbose bool
}

func (o *Options) emit(msg any) {
	if o.Emit != nil {
		o.Emit(msg)
	}
}

// Run starts the pipeline and blocks until ctx is cancelled and every stage has
// drained (§3.2 shutdown order): source stops → segmenter flushes → workers
// drain → dispatcher drains. The DB is closed by the caller.
func Run(ctx context.Context, o Options) error {
	blocks, err := o.Source.Start(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = o.Source.Stop() }()

	// Surface an unexpected rtl_fm exit as a status warning (§4.3).
	if fs, ok := o.Source.(interface{ Fatal() <-chan error }); ok {
		go func() {
			select {
			case err := <-fs.Fatal():
				if err != nil {
					o.emit(events.StatusMsg{Text: err.Error(), Warn: true})
				}
			case <-ctx.Done():
			}
		}()
	}

	seg := audio.NewSegmenter(o.Seg, o.Emit)
	segments := seg.Run(ctx, blocks)

	if o.Dispatcher != nil {
		go o.Dispatcher.Run(ctx)
	}

	workers := o.Workers
	if workers < 1 {
		workers = 1
	}
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			o.worker(ctx, segments)
		}()
	}
	for i := 0; i < workers; i++ {
		<-done
	}
	return nil
}

// worker consumes closed segments, transcribes, filters, persists, and submits
// alerts. It owns DB writes for its segments; the store serializes across
// workers (§3.2).
func (o Options) worker(ctx context.Context, segments <-chan audio.Segment) {
	tr, err := o.NewTranscriber()
	if err != nil {
		o.emit(events.StatusMsg{Text: "transcriber unavailable: " + err.Error(), Warn: true})
		// Drain segments so the segmenter is not blocked on shutdown.
		for range segments {
		}
		return
	}
	defer tr.Close()

	for seg := range segments {
		o.emit(events.TranscribingMsg{QueueDepth: len(segments) + 1})
		// Process on a drain context, NOT the capture context: when the capture
		// context is cancelled (SIGINT/quit) the segmenter flushes its open
		// segment, and this worker must still transcribe and persist it (§3.2
		// shutdown order: source stops → segmenter flushes → transcriber
		// drains). Bounded so a stuck write cannot hang shutdown forever.
		pctx, cancel := context.WithTimeout(context.Background(), drainCap)
		o.process(pctx, tr, seg)
		cancel()
		o.emit(events.TranscribingMsg{QueueDepth: len(segments)})
	}
}

// drainCap bounds per-segment processing during shutdown. Generous enough not
// to truncate a legitimate transcription of a max-length segment.
const drainCap = 3 * time.Minute

func (o Options) process(ctx context.Context, tr asr.Transcriber, seg audio.Segment) {
	freq := o.Source.FrequencyHz()

	audioPath := ""
	if o.RetainAudio && o.AudioDir != "" {
		p := audio.AudioPath(o.AudioDir, seg.StartedAt)
		if err := audio.WriteWAV(p, seg.Samples); err != nil {
			slog.Warn("wav write failed", "err", err)
		} else {
			audioPath = p
		}
	}

	id, err := o.Store.InsertPending(ctx, db.TransmissionMeta{
		StartedAt:   seg.StartedAt,
		EndedAt:     seg.StartedAt.Add(seg.Duration),
		Duration:    seg.Duration,
		Source:      o.Source.Descriptor(),
		FrequencyHz: freq,
		AudioPath:   audioPath,
		Model:       o.Model,
		Capped:      seg.Capped,
	})
	if err != nil {
		slog.Error("insert pending", "err", err)
		return
	}

	res, err := tr.Transcribe(seg.Samples)
	if err != nil {
		_ = o.Store.SetError(ctx, id, err.Error())
		o.emit(events.TransmissionMsg{Transmission: events.Transmission{
			ID: id, StartedAt: seg.StartedAt, Duration: seg.Duration,
			Source: o.Source.Descriptor(), FrequencyHz: freq, Status: "error", Capped: seg.Capped,
		}})
		return
	}

	kept, finalText, status, reason := asr.Apply(res.Segments, o.FilterCfg)
	avgLP, noSpeech, compRatio := asr.Aggregate(kept, finalText)

	// Persist surviving segments with absolute-timeline offsets (§6).
	for _, s := range kept {
		lp := s.AvgLogprob
		_ = o.Store.InsertSegment(ctx, id, db.Segment{
			StartS: s.Start, EndS: s.End, Text: s.Text, AvgLogprob: &lp,
		})
	}

	avgLPPtr := ptrIfKept(avgLP, len(kept))
	_ = o.Store.FinishTranscription(ctx, id, db.TranscriptionResult{
		Text:             finalText,
		Language:         res.Language,
		AvgLogprob:       avgLPPtr,
		NoSpeechProb:     ptrIfKept(noSpeech, len(kept)),
		CompressionRatio: &compRatio,
		Status:           status,
		FilterReason:     reason,
	})

	// Alert matching (synchronous, cheap) for the emitted row; delivery is async
	// via the dispatcher and never blocks the pipeline (§7, §2.11).
	var alerted bool
	var alertRules []string
	if status == "transcribed" && len(o.Rules) > 0 {
		if m, _, ok := alerts.MatchAll(o.Rules, finalText); ok {
			alerted = true
			alertRules = m.Rules
			if o.Dispatcher != nil {
				o.Dispatcher.Submit(alerts.Event{
					TransmissionID: id, Text: finalText, StartedAt: seg.StartedAt,
				})
			}
		}
	}

	o.emit(events.TransmissionMsg{Transmission: events.Transmission{
		ID: id, StartedAt: seg.StartedAt, Duration: seg.Duration,
		Source: o.Source.Descriptor(), FrequencyHz: freq, Text: finalText,
		Status: status, FilterReason: reason, AvgLogprob: avgLPPtr, Capped: seg.Capped,
		Alerted: alerted, AlertRules: alertRules,
	}})
}

func ptrIfKept(v float64, n int) *float64 {
	if n == 0 {
		return nil
	}
	return &v
}

// PruneAudio removes WAVs older than retentionDays at startup (§5).
func PruneAudio(dir string, retentionDays int) (int, error) {
	if dir == "" || retentionDays <= 0 {
		return 0, nil
	}
	return audio.Prune(dir, retentionDays, time.Now())
}
