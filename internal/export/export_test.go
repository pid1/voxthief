package export

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/pid1/voxthief/internal/db/gen"
)

func sampleRows() []gen.Transmission {
	return []gen.Transmission{
		{
			ID: 1, StartedAt: 1_700_000_000, DurationS: 4.2, Source: "rtlsdr:146520000@dev0",
			FrequencyHz: sql.NullInt64{Int64: 146520000, Valid: true},
			Text:        sql.NullString{String: "unit 12 en route", Valid: true},
			Status:      "transcribed",
		},
		{
			ID: 2, StartedAt: 1_700_000_100, DurationS: 1.1, Source: "soundcard:default",
			Status: "filtered", FilterReason: sql.NullString{String: "vad_no_speech", Valid: true},
		},
	}
}

func TestWriteJSONL(t *testing.T) {
	var b strings.Builder
	if err := Write(&b, "jsonl", sampleRows()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 jsonl lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], `"frequency_hz":146520000`) {
		t.Errorf("freq missing: %s", lines[0])
	}
	if !strings.Contains(lines[0], `"text":"unit 12 en route"`) {
		t.Errorf("text missing: %s", lines[0])
	}
}

func TestWriteCSV(t *testing.T) {
	var b strings.Builder
	if err := Write(&b, "csv", sampleRows()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.HasPrefix(out, "id,started_at,duration_s,source,frequency_hz,status,text") {
		t.Errorf("missing header: %q", out)
	}
	if !strings.Contains(out, "146520000") {
		t.Errorf("freq not in csv")
	}
}

func TestWriteTXT(t *testing.T) {
	var b strings.Builder
	if err := Write(&b, "txt", sampleRows()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "── filtered ──") {
		t.Errorf("filtered marker missing in txt: %q", b.String())
	}
}

func TestUnknownFormat(t *testing.T) {
	if err := Write(&strings.Builder{}, "xml", nil); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	got, err := ParseSince("24h", now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("24h = %v", got)
	}
	got, err = ParseSince("7d", now)
	if err != nil || !got.Equal(now.Add(-7*24*time.Hour)) {
		t.Errorf("7d = %v err=%v", got, err)
	}
	got, err = ParseSince("2026-07-01", now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Year() != 2026 || got.Month() != 7 || got.Day() != 1 {
		t.Errorf("iso date = %v", got)
	}
	if _, err := ParseSince("garbage", now); err == nil {
		t.Error("expected parse error")
	}
}
