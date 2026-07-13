package alerts

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/pid1/voxthief/internal/db"
	"github.com/pid1/voxthief/internal/events"
)

// Recorder is the subset of the store the dispatcher writes to. *db.Store
// satisfies it; tests supply a fake (§3.2 — bookkeeping writes go through the
// same store, serialized behind its connection).
type Recorder interface {
	InsertAlert(ctx context.Context, a db.AlertRecord) error
	CountAlertsSentSince(ctx context.Context, since time.Time) (int, error)
	LastFireForRule(ctx context.Context, rule string) (time.Time, error)
}

// chanCap is the bounded dispatcher queue (§3.2). On overflow the alert is
// dropped and counted, never blocking the pipeline.
const chanCap = 64

// Event is submitted by the transcriber AFTER a status='transcribed' row is
// committed (filtered rows never alert, §7.1).
type Event struct {
	TransmissionID int64
	Text           string
	StartedAt      time.Time // transmission air time
}

// Dispatcher matches events against rules and delivers Pushover notifications
// asynchronously, applying cooldown and the global hourly cap (§7.2).
type Dispatcher struct {
	client     *Client
	rules      []Rule
	rec        Recorder
	maxPerHour int
	emit       func(events.AlertMsg)

	ch      chan Event
	dropped atomic.Int64

	now      func() time.Time
	lastFire map[string]time.Time // per-rule last successful send (cooldown)
	sends    []time.Time          // sliding window of send times (hourly cap)
}

// NewDispatcher builds a Dispatcher. emit may be nil.
func NewDispatcher(client *Client, rules []Rule, rec Recorder, maxPerHour int, emit func(events.AlertMsg)) *Dispatcher {
	if emit == nil {
		emit = func(events.AlertMsg) {}
	}
	return &Dispatcher{
		client:     client,
		rules:      rules,
		rec:        rec,
		maxPerHour: maxPerHour,
		emit:       emit,
		ch:         make(chan Event, chanCap),
		now:        time.Now,
		lastFire:   map[string]time.Time{},
	}
}

// Submit enqueues e without blocking. On a full queue the alert is dropped and
// counted (§3.2) so the pipeline is never slowed.
func (d *Dispatcher) Submit(e Event) {
	select {
	case d.ch <- e:
	default:
		d.dropped.Add(1)
	}
}

// Dropped returns the number of alerts dropped due to a full queue.
func (d *Dispatcher) Dropped() int64 { return d.dropped.Load() }

// Run processes the queue until ctx is cancelled and the channel drains. The
// loop's ctx only decides WHEN to stop; each event is handled on a fresh
// bounded context (handleCtx) so an alert flushed during shutdown still
// delivers and records rather than failing with "context canceled" (§3.2:
// dispatcher drains in-flight sends).
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case e := <-d.ch:
			d.handleDetached(e)
		case <-ctx.Done():
			// Drain any in-flight events before returning (§3.2 shutdown).
			for {
				select {
				case e := <-d.ch:
					d.handleDetached(e)
				default:
					return
				}
			}
		}
	}
}

// drainCap bounds a single alert's delivery so shutdown cannot hang.
const drainCap = 40 * time.Second

func (d *Dispatcher) handleDetached(e Event) {
	hctx, cancel := context.WithTimeout(context.Background(), drainCap)
	defer cancel()
	d.handle(hctx, e)
}

func (d *Dispatcher) handle(ctx context.Context, e Event) {
	m, matched, ok := MatchAll(d.rules, e.Text)
	if !ok {
		return
	}
	now := d.now()

	// Per-rule cooldown: suppress if ANY matched rule is still cooling down.
	for _, r := range matched {
		if r.CooldownS <= 0 {
			continue
		}
		if last, seen := d.lastFire[r.Name]; seen && now.Sub(last) < time.Duration(r.CooldownS)*time.Second {
			d.record(ctx, e, m, "suppressed", "cooldown", nil, "")
			d.emit(events.AlertMsg{Rules: m.Rules, Status: "suppressed", Reason: "cooldown"})
			return
		}
	}

	// Global hourly sliding-window cap (§7.2, §16.6).
	if d.maxPerHour > 0 {
		d.pruneWindow(now)
		if len(d.sends) >= d.maxPerHour {
			d.record(ctx, e, m, "suppressed", "hourly_cap", nil, "")
			d.emit(events.AlertMsg{Rules: m.Rules, Status: "suppressed", Reason: "hourly_cap"})
			return
		}
	}

	// Deliver.
	status, err := d.client.Send(ctx, Message{
		Message:   e.Text,
		Title:     m.Title,
		Timestamp: e.StartedAt,
		Priority:  m.Priority,
		Sound:     m.Sound,
		Retry:     m.Retry,
		Expire:    m.Expire,
	})
	httpStatus := status
	if err != nil {
		d.record(ctx, e, m, "failed", "", &httpStatus, err.Error())
		d.emit(events.AlertMsg{Rules: m.Rules, Status: "failed", Reason: err.Error()})
		return
	}

	sentAt := now
	for _, r := range matched {
		d.lastFire[r.Name] = sentAt
	}
	d.sends = append(d.sends, sentAt)
	d.record(ctx, e, m, "sent", "", &httpStatus, "")
	d.emit(events.AlertMsg{Rules: m.Rules, Status: "sent"})
}

func (d *Dispatcher) pruneWindow(now time.Time) {
	cutoff := now.Add(-time.Hour)
	i := 0
	for i < len(d.sends) && d.sends[i].Before(cutoff) {
		i++
	}
	d.sends = d.sends[i:]
}

func (d *Dispatcher) record(ctx context.Context, e Event, m Match, status, suppressReason string, httpStatus *int, errMsg string) {
	rec := db.AlertRecord{
		TransmissionID: e.TransmissionID,
		RuleNames:      joinRules(m.Rules),
		Status:         status,
		SuppressReason: suppressReason,
		HTTPStatus:     httpStatus,
		Error:          errMsg,
	}
	if status == "sent" {
		now := d.now()
		rec.SentAt = &now
	}
	// Best-effort: a bookkeeping write must not crash the dispatcher.
	_ = d.rec.InsertAlert(ctx, rec)
}

func joinRules(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}
