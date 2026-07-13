package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pid1/voxthief/internal/events"
)

// headlessRecord is the one-object-per-transmission JSON emitted on stdout in
// headless mode (§10).
type headlessRecord struct {
	StartedAt   string   `json:"started_at"` // RFC3339 UTC
	DurationS   float64  `json:"duration_s"`
	Source      string   `json:"source"`
	FrequencyHz *int64   `json:"frequency_hz"`
	Text        string   `json:"text"`
	Status      string   `json:"status"`
	AvgLogprob  *float64 `json:"avg_logprob"`
	Alerted     bool     `json:"alerted"`
	AlertRules  []string `json:"alert_rules"`
}

// NewHeadlessEmitter returns an Emit callback for headless mode: one JSON object
// per finished transmission on out; squelch/level events go to errw only when
// verbose (§10). The returned func is safe for concurrent use.
func NewHeadlessEmitter(out, errw io.Writer, verbose bool) func(any) {
	var mu sync.Mutex
	enc := json.NewEncoder(out)
	return func(msg any) {
		mu.Lock()
		defer mu.Unlock()
		switch m := msg.(type) {
		case events.TransmissionMsg:
			rec := headlessRecord{
				StartedAt:   m.StartedAt.UTC().Format(time.RFC3339Nano),
				DurationS:   m.Duration.Seconds(),
				Source:      m.Source,
				FrequencyHz: m.FrequencyHz,
				Text:        m.Text,
				Status:      m.Status,
				AvgLogprob:  m.AvgLogprob,
				Alerted:     m.Alerted,
				AlertRules:  m.AlertRules,
			}
			if rec.AlertRules == nil {
				rec.AlertRules = []string{}
			}
			_ = enc.Encode(rec)
		case events.StatusMsg:
			if m.Warn {
				fmt.Fprintln(errw, "warning:", m.Text)
			}
		case events.SquelchOpenedMsg, events.SquelchClosedMsg, events.LevelMsg:
			if verbose {
				fmt.Fprintf(errw, "%T %+v\n", m, m)
			}
		}
	}
}
