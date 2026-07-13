package asr

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := map[string]string{
		"Thank you.":           "thank you",
		"  PLEASE  Subscribe!": "please subscribe",
		"You":                  "you",
		".":                    "",
		"Unit-12, en route.":   "unit12 en route",
	}
	for in, want := range tests {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGzipRatio(t *testing.T) {
	repetitive := strings.Repeat("thank you ", 50)
	varied := "county ems respond to the 1400 block of main street for a report of a fall"
	if GzipRatio(repetitive) <= GzipRatio(varied) {
		t.Errorf("repetitive ratio %.2f should exceed varied ratio %.2f",
			GzipRatio(repetitive), GzipRatio(varied))
	}
	if GzipRatio("") != 0 {
		t.Errorf("empty ratio should be 0")
	}
}

func TestFilterThresholds(t *testing.T) {
	cfg := DefaultFilterConfig()
	good := Segment{Text: "unit twelve en route to the scene", AvgLogprob: -0.4, NoSpeechProb: 0.1}

	tests := []struct {
		name string
		seg  Segment
		drop string // expected reason, "" = kept
	}{
		{"kept", good, ""},
		{"no_speech", Segment{Text: good.Text, AvgLogprob: -0.4, NoSpeechProb: 0.7}, "no_speech"},
		{"low_logprob", Segment{Text: good.Text, AvgLogprob: -1.5, NoSpeechProb: 0.1}, "low_logprob"},
		{"high_compression", Segment{Text: strings.Repeat("na ", 200), AvgLogprob: -0.4, NoSpeechProb: 0.1}, "high_compression"},
		{"blocklist", Segment{Text: "Thank you.", AvgLogprob: -0.4, NoSpeechProb: 0.1}, "blocklist"},
		{"blocklist_you", Segment{Text: "you", AvgLogprob: -0.4, NoSpeechProb: 0.1}, "blocklist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.dropReason(tt.seg); got != tt.drop {
				t.Errorf("dropReason = %q, want %q", got, tt.drop)
			}
		})
	}
}

func TestApplyAllDroppedIsFiltered(t *testing.T) {
	cfg := DefaultFilterConfig()
	segs := []Segment{
		{Text: "thank you", AvgLogprob: -0.2, NoSpeechProb: 0.1},            // blocklist
		{Text: "thanks for watching", AvgLogprob: -0.2, NoSpeechProb: 0.1},  // blocklist
		{Text: "real speech here now", AvgLogprob: -0.2, NoSpeechProb: 0.9}, // no_speech
	}
	kept, text, status, reason := Apply(segs, cfg)
	if len(kept) != 0 || text != "" {
		t.Fatalf("expected all dropped, got kept=%d text=%q", len(kept), text)
	}
	if status != "filtered" {
		t.Errorf("status = %q, want filtered", status)
	}
	if reason != "blocklist" { // 2 blocklist vs 1 no_speech → dominant blocklist
		t.Errorf("dominant reason = %q, want blocklist", reason)
	}
}

func TestApplyZeroSegmentsVADReason(t *testing.T) {
	_, _, status, reason := Apply(nil, DefaultFilterConfig())
	if status != "filtered" || reason != "vad_no_speech" {
		t.Errorf("empty input: status=%q reason=%q, want filtered/vad_no_speech", status, reason)
	}
}

func TestApplyKeepsAndJoins(t *testing.T) {
	cfg := DefaultFilterConfig()
	segs := []Segment{
		{Text: "county ems", AvgLogprob: -0.3, NoSpeechProb: 0.1},
		{Text: "thank you", AvgLogprob: -0.3, NoSpeechProb: 0.1}, // dropped
		{Text: "respond to main street", AvgLogprob: -0.3, NoSpeechProb: 0.1},
	}
	kept, text, status, reason := Apply(segs, cfg)
	if len(kept) != 2 || status != "transcribed" || reason != "" {
		t.Fatalf("kept=%d status=%q reason=%q", len(kept), status, reason)
	}
	if text != "county ems respond to main street" {
		t.Errorf("final text = %q", text)
	}
}

func TestUserBlocklistExtends(t *testing.T) {
	cfg := DefaultFilterConfig("Radio Check")
	if cfg.dropReason(Segment{Text: "radio check!", AvgLogprob: -0.2, NoSpeechProb: 0.1}) != "blocklist" {
		t.Errorf("user blocklist entry not applied")
	}
}

func TestAggregate(t *testing.T) {
	kept := []Segment{
		{Text: "a", AvgLogprob: -0.2, NoSpeechProb: 0.1},
		{Text: "b", AvgLogprob: -0.4, NoSpeechProb: 0.3},
	}
	lp, ns, _ := Aggregate(kept, "a b")
	if lp < -0.31 || lp > -0.29 {
		t.Errorf("mean logprob = %v, want ~-0.3", lp)
	}
	if ns < 0.19 || ns > 0.21 {
		t.Errorf("mean no_speech = %v, want ~0.2", ns)
	}
}
