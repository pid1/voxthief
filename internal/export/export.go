// Package export renders stored transmissions to jsonl, csv, or txt (§11).
package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/pid1/voxthief/internal/db"
	"github.com/pid1/voxthief/internal/db/gen"
)

// Record is the flat, serialization-friendly view of a transmission row.
type Record struct {
	ID          int64    `json:"id"`
	StartedAt   string   `json:"started_at"` // RFC3339 UTC
	DurationS   float64  `json:"duration_s"`
	Source      string   `json:"source"`
	FrequencyHz *int64   `json:"frequency_hz,omitempty"`
	Text        string   `json:"text"`
	Status      string   `json:"status"`
	Language    string   `json:"language,omitempty"`
	AvgLogprob  *float64 `json:"avg_logprob,omitempty"`
}

func toRecord(t gen.Transmission) Record {
	r := Record{
		ID:        t.ID,
		StartedAt: db.FromUnix(t.StartedAt).Format(time.RFC3339Nano),
		DurationS: t.DurationS,
		Source:    t.Source,
		Text:      t.Text.String,
		Status:    t.Status,
		Language:  t.Language.String,
	}
	if t.FrequencyHz.Valid {
		v := t.FrequencyHz.Int64
		r.FrequencyHz = &v
	}
	if t.AvgLogprob.Valid {
		v := t.AvgLogprob.Float64
		r.AvgLogprob = &v
	}
	return r
}

// Write renders rows to w in the given format ("jsonl", "csv", "txt").
func Write(w io.Writer, format string, rows []gen.Transmission) error {
	switch format {
	case "jsonl", "":
		return writeJSONL(w, rows)
	case "csv":
		return writeCSV(w, rows)
	case "txt":
		return writeTXT(w, rows)
	default:
		return fmt.Errorf("unknown export format %q (want jsonl|csv|txt)", format)
	}
}

func writeJSONL(w io.Writer, rows []gen.Transmission) error {
	enc := json.NewEncoder(w)
	for _, t := range rows {
		if err := enc.Encode(toRecord(t)); err != nil {
			return err
		}
	}
	return nil
}

func writeCSV(w io.Writer, rows []gen.Transmission) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"id", "started_at", "duration_s", "source", "frequency_hz", "status", "text"}); err != nil {
		return err
	}
	for _, t := range rows {
		r := toRecord(t)
		freq := ""
		if r.FrequencyHz != nil {
			freq = strconv.FormatInt(*r.FrequencyHz, 10)
		}
		if err := cw.Write([]string{
			strconv.FormatInt(r.ID, 10),
			r.StartedAt,
			strconv.FormatFloat(r.DurationS, 'f', 2, 64),
			r.Source,
			freq,
			r.Status,
			r.Text,
		}); err != nil {
			return err
		}
	}
	return cw.Error()
}

func writeTXT(w io.Writer, rows []gen.Transmission) error {
	for _, t := range rows {
		r := toRecord(t)
		local := db.FromUnix(t.StartedAt).Local().Format("2006-01-02 15:04:05")
		text := r.Text
		if r.Status != "transcribed" {
			text = "── " + r.Status + " ──"
		}
		if _, err := fmt.Fprintf(w, "%s  (%.1fs)  %s\n", local, r.DurationS, text); err != nil {
			return err
		}
	}
	return nil
}

// ParseSince parses an ISO/RFC3339 timestamp or a relative duration like "24h",
// "7d", "90m" (relative to now).
func ParseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty --since")
	}
	if d, err := parseRelative(s); err == nil {
		return now.Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse --since %q (want RFC3339 or a duration like 24h/7d)", s)
}

// parseRelative extends time.ParseDuration with a "d" (day) suffix.
func parseRelative(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(n * 24 * float64(time.Hour)), nil
	}
	return time.ParseDuration(s)
}
