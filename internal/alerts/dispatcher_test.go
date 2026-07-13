package alerts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pid1/voxthief/internal/config"
	"github.com/pid1/voxthief/internal/db"
)

// fakeRecorder captures InsertAlert calls for assertions.
type fakeRecorder struct {
	mu      sync.Mutex
	records []db.AlertRecord
}

func (f *fakeRecorder) InsertAlert(_ context.Context, a db.AlertRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, a)
	return nil
}
func (f *fakeRecorder) CountAlertsSentSince(context.Context, time.Time) (int, error) { return 0, nil }
func (f *fakeRecorder) LastFireForRule(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}
func (f *fakeRecorder) byStatus(status string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.records {
		if r.Status == status {
			n++
		}
	}
	return n
}

func okServer(hits *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
}

func testClientTo(url string) *Client {
	c := NewClient("t", "u")
	c.BaseURL = url
	c.sleep = func(time.Duration) {}
	return c
}

func TestDispatcherSingleNotificationMultiRule(t *testing.T) {
	var hits int32
	srv := okServer(&hits)
	defer srv.Close()
	rules, _ := Compile([]config.RuleConfig{
		{Name: "a", Pattern: "alpha"},
		{Name: "b", Pattern: "bravo"},
	})
	rec := &fakeRecorder{}
	d := NewDispatcher(testClientTo(srv.URL), rules, rec, 0, nil)
	d.handle(context.Background(), Event{TransmissionID: 1, Text: "alpha bravo", StartedAt: time.Unix(100, 0)})

	if hits != 1 {
		t.Errorf("expected exactly 1 Pushover send for multi-rule match, got %d", hits)
	}
	if rec.byStatus("sent") != 1 {
		t.Errorf("expected 1 sent record, got %d", rec.byStatus("sent"))
	}
	if rec.records[0].RuleNames != "a,b" {
		t.Errorf("rule_names = %q, want a,b", rec.records[0].RuleNames)
	}
}

func TestDispatcherNoMatchNoAlert(t *testing.T) {
	var hits int32
	srv := okServer(&hits)
	defer srv.Close()
	rules, _ := Compile([]config.RuleConfig{{Name: "a", Pattern: "alpha"}})
	rec := &fakeRecorder{}
	d := NewDispatcher(testClientTo(srv.URL), rules, rec, 0, nil)
	d.handle(context.Background(), Event{TransmissionID: 1, Text: "nothing here"})
	if hits != 0 || len(rec.records) != 0 {
		t.Errorf("no-match should produce no alert (hits=%d records=%d)", hits, len(rec.records))
	}
}

func TestDispatcherCooldownSuppression(t *testing.T) {
	var hits int32
	srv := okServer(&hits)
	defer srv.Close()
	rules, _ := Compile([]config.RuleConfig{{Name: "a", Pattern: "alpha", CooldownS: 60}})
	rec := &fakeRecorder{}
	d := NewDispatcher(testClientTo(srv.URL), rules, rec, 0, nil)

	base := time.Unix(1000, 0)
	d.now = func() time.Time { return base }
	d.handle(context.Background(), Event{TransmissionID: 1, Text: "alpha"}) // sent
	d.now = func() time.Time { return base.Add(30 * time.Second) }          // within cooldown
	d.handle(context.Background(), Event{TransmissionID: 2, Text: "alpha"}) // suppressed

	if hits != 1 {
		t.Errorf("expected 1 send (second suppressed by cooldown), got %d", hits)
	}
	if rec.byStatus("sent") != 1 || rec.byStatus("suppressed") != 1 {
		t.Errorf("want 1 sent + 1 suppressed, got sent=%d suppressed=%d",
			rec.byStatus("sent"), rec.byStatus("suppressed"))
	}
	// suppress_reason recorded.
	var found bool
	for _, r := range rec.records {
		if r.Status == "suppressed" && r.SuppressReason == "cooldown" {
			found = true
		}
	}
	if !found {
		t.Error("cooldown suppression not recorded with reason=cooldown")
	}
}

func TestDispatcherHourlyCapExhaustion(t *testing.T) {
	// A misfiring .*-style rule must exhaust the hourly cap, not blow past it (§16.6).
	var hits int32
	srv := okServer(&hits)
	defer srv.Close()
	rules, _ := Compile([]config.RuleConfig{{Name: "greedy", Pattern: ".*"}})
	rec := &fakeRecorder{}
	const cap = 5
	d := NewDispatcher(testClientTo(srv.URL), rules, rec, cap, nil)
	base := time.Unix(1000, 0)
	d.now = func() time.Time { return base }

	for i := 0; i < 20; i++ {
		d.handle(context.Background(), Event{TransmissionID: int64(i), Text: "anything"})
	}
	if int(hits) != cap {
		t.Errorf("expected exactly %d sends (cap), got %d", cap, hits)
	}
	if rec.byStatus("sent") != cap {
		t.Errorf("sent records = %d, want %d", rec.byStatus("sent"), cap)
	}
	if rec.byStatus("suppressed") != 20-cap {
		t.Errorf("suppressed records = %d, want %d", rec.byStatus("suppressed"), 20-cap)
	}
	// After an hour, the window clears and sends resume.
	d.now = func() time.Time { return base.Add(2 * time.Hour) }
	d.handle(context.Background(), Event{TransmissionID: 99, Text: "again"})
	if int(hits) != cap+1 {
		t.Errorf("expected send to resume after window clears, hits=%d", hits)
	}
}

func TestDispatcherSubmitDropsWhenFull(t *testing.T) {
	rules, _ := Compile([]config.RuleConfig{{Name: "a", Pattern: "alpha"}})
	d := NewDispatcher(NewClient("t", "u"), rules, &fakeRecorder{}, 0, nil)
	// Do not Run the dispatcher, so the channel fills.
	for i := 0; i < chanCap+50; i++ {
		d.Submit(Event{TransmissionID: int64(i), Text: "alpha"})
	}
	if d.Dropped() != 50 {
		t.Errorf("expected 50 drops, got %d", d.Dropped())
	}
}

func TestDispatcherFailedRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"status":0,"errors":["bad"]}`))
	}))
	defer srv.Close()
	rules, _ := Compile([]config.RuleConfig{{Name: "a", Pattern: "alpha"}})
	rec := &fakeRecorder{}
	d := NewDispatcher(testClientTo(srv.URL), rules, rec, 0, nil)
	d.handle(context.Background(), Event{TransmissionID: 1, Text: "alpha"})
	if rec.byStatus("failed") != 1 {
		t.Errorf("expected 1 failed record, got %d", rec.byStatus("failed"))
	}
}
