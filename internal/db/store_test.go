package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openMigrated(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "voxthief.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Upgrade(context.Background()); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	return s
}

func TestMigrateToHead(t *testing.T) {
	s := openMigrated(t)
	current, head, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if head != 2 {
		t.Fatalf("want head 2 (two migrations), got %d", head)
	}
	if current != head {
		t.Fatalf("want current==head, got current=%d head=%d", current, head)
	}
	if err := s.EnsureHead(context.Background()); err != nil {
		t.Fatalf("EnsureHead: %v", err)
	}
}

func TestInsertAndFinishRoundTrip(t *testing.T) {
	s := openMigrated(t)
	ctx := context.Background()
	freq := int64(146520000)
	id, err := s.InsertPending(ctx, TransmissionMeta{
		StartedAt:   time.Unix(1000, 0).UTC(),
		EndedAt:     time.Unix(1004, 0).UTC(),
		Duration:    4 * time.Second,
		Source:      "rtlsdr:146520000@dev0",
		FrequencyHz: &freq,
		Model:       "small.en-q8_0",
	})
	if err != nil {
		t.Fatal(err)
	}
	lp := -0.5
	if err := s.FinishTranscription(ctx, id, TranscriptionResult{
		Text: "unit 12 en route", Language: "en", AvgLogprob: &lp, Status: "transcribed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertSegment(ctx, id, Segment{StartS: 0, EndS: 4, Text: "unit 12 en route", AvgLogprob: &lp}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTransmission(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text.String != "unit 12 en route" || got.Status != "transcribed" {
		t.Fatalf("bad row: %+v", got)
	}
	if !got.FrequencyHz.Valid || got.FrequencyHz.Int64 != freq {
		t.Fatalf("freq not stored: %+v", got.FrequencyHz)
	}
}

func TestAlertFKCascade(t *testing.T) {
	s := openMigrated(t)
	ctx := context.Background()
	id, err := s.InsertPending(ctx, TransmissionMeta{
		StartedAt: time.Unix(1000, 0).UTC(), EndedAt: time.Unix(1001, 0).UTC(),
		Duration: time.Second, Source: "soundcard:default", Model: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1005, 0).UTC()
	http := 200
	if err := s.InsertAlert(ctx, AlertRecord{
		TransmissionID: id, RuleNames: "callsign", SentAt: &now, Status: "sent", HTTPStatus: &http,
	}); err != nil {
		t.Fatal(err)
	}
	// Deleting the transmission must cascade to alerts and segments.
	if _, err := s.db.ExecContext(ctx, "DELETE FROM transmissions WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("FK cascade failed: %d alerts remain", n)
	}
}

func TestHourlyCapAndCooldownQueries(t *testing.T) {
	s := openMigrated(t)
	ctx := context.Background()
	id, _ := s.InsertPending(ctx, TransmissionMeta{
		StartedAt: time.Unix(1000, 0).UTC(), EndedAt: time.Unix(1001, 0).UTC(),
		Duration: time.Second, Source: "x", Model: "m",
	})
	base := time.Unix(2000, 0).UTC()
	http := 200
	for i := 0; i < 3; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		if err := s.InsertAlert(ctx, AlertRecord{
			TransmissionID: id, RuleNames: "callsign", SentAt: &at, Status: "sent", HTTPStatus: &http,
		}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.CountAlertsSentSince(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("want 3 sent in window, got %d", n)
	}
	last, err := s.LastFireForRule(ctx, "callsign")
	if err != nil {
		t.Fatal(err)
	}
	if last.Unix() != base.Add(2*time.Minute).Unix() {
		t.Fatalf("want last fire at +2m, got %v", last)
	}
}

func TestMigrateDownUpRoundTrip(t *testing.T) {
	s := openMigrated(t)
	ctx := context.Background()
	p, err := provider(s.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if v, _ := p.GetDBVersion(ctx); v != 0 {
		t.Fatalf("want version 0 after down, got %d", v)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if v, _ := p.GetDBVersion(ctx); v != 2 {
		t.Fatalf("want version 2 after re-up, got %d", v)
	}
}
