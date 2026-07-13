package asr

import (
	"bytes"
	"compress/gzip"
	"sort"
	"strings"
	"unicode"
)

// FilterConfig holds the hallucination-filter thresholds (§6). Filters run per
// returned segment, then over the whole transmission; they run BEFORE alert
// matching by design (§16.12).
type FilterConfig struct {
	NoSpeechMax   float64  // drop segment if no_speech_prob > this
	MinAvgLogprob float64  // drop segment if avg token logprob < this
	MaxGzipRatio  float64  // drop segment if gzip compression ratio > this
	Blocklist     []string // normalized phrases to drop
}

// DefaultBlocklist is the built-in hallucination blocklist (§6), stored
// normalized. Users extend it via config.
var DefaultBlocklist = []string{
	"thank you",
	"thanks for watching",
	"thank you for watching",
	"please subscribe",
	"you",
	".",
}

// DefaultFilterConfig returns the §6 defaults. userBlocklist entries are
// normalized and appended to the built-in list.
func DefaultFilterConfig(userBlocklist ...string) FilterConfig {
	bl := make([]string, 0, len(DefaultBlocklist)+len(userBlocklist))
	for _, s := range DefaultBlocklist {
		bl = append(bl, Normalize(s))
	}
	for _, s := range userBlocklist {
		if n := Normalize(s); n != "" {
			bl = append(bl, n)
		}
	}
	return FilterConfig{
		NoSpeechMax:   0.66,
		MinAvgLogprob: -1.0,
		MaxGzipRatio:  2.4,
		Blocklist:     bl,
	}
}

// Normalize casefolds, strips punctuation, and collapses whitespace so blocklist
// comparison ignores formatting (§6).
func Normalize(text string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsSpace(r):
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			// dropped
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// GzipRatio is len(text)/len(gzip(text)); a high ratio flags the repetitive
// output typical of whisper hallucinations (§6). Empty/tiny text returns 0.
func GzipRatio(text string) float64 {
	if len(text) == 0 {
		return 0
	}
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	_, _ = w.Write([]byte(text))
	_ = w.Close()
	if buf.Len() == 0 {
		return 0
	}
	return float64(len(text)) / float64(buf.Len())
}

// dropReason returns the reason a segment is dropped, or "" if it is kept.
// Reasons are checked in a fixed priority order so "dominant reason" is stable.
func (c FilterConfig) dropReason(s Segment) string {
	if s.NoSpeechProb > c.NoSpeechMax {
		return "no_speech"
	}
	if s.AvgLogprob < c.MinAvgLogprob {
		return "low_logprob"
	}
	if GzipRatio(s.Text) > c.MaxGzipRatio {
		return "high_compression"
	}
	norm := Normalize(s.Text)
	if norm == "" {
		return "blocklist"
	}
	for _, b := range c.Blocklist {
		if norm == b {
			return "blocklist"
		}
	}
	return ""
}

// Apply runs the filters over segs and returns the surviving segments, the
// joined final text, the transmission status ("transcribed" or "filtered"), and
// — when everything is dropped — the dominant drop reason (§6).
func Apply(segs []Segment, cfg FilterConfig) (kept []Segment, finalText, status, filterReason string) {
	reasonCounts := map[string]int{}
	for _, s := range segs {
		if r := cfg.dropReason(s); r != "" {
			reasonCounts[r]++
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		return nil, "", "filtered", dominantReason(reasonCounts)
	}
	parts := make([]string, 0, len(kept))
	for _, s := range kept {
		if t := strings.TrimSpace(s.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return kept, strings.Join(parts, " "), "transcribed", ""
}

// dominantReason returns the most frequent drop reason; ties break by the fixed
// priority order no_speech > low_logprob > high_compression > blocklist. Empty
// input (whisper returned zero segments) yields "vad_no_speech" (§6).
func dominantReason(counts map[string]int) string {
	if len(counts) == 0 {
		return "vad_no_speech"
	}
	order := map[string]int{"no_speech": 0, "low_logprob": 1, "high_compression": 2, "blocklist": 3}
	type kv struct {
		reason string
		n      int
	}
	var all []kv
	for r, n := range counts {
		all = append(all, kv{r, n})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return order[all[i].reason] < order[all[j].reason]
	})
	return all[0].reason
}

// Aggregate computes the whole-transmission stats persisted on the row (§8):
// mean segment avg_logprob, mean segment no_speech_prob, and the gzip
// compression ratio of the final text. Returns zeros for an empty transmission.
func Aggregate(kept []Segment, finalText string) (avgLogprob, noSpeechProb, compressionRatio float64) {
	if len(kept) == 0 {
		return 0, 0, GzipRatio(finalText)
	}
	var sumLP, sumNS float64
	for _, s := range kept {
		sumLP += s.AvgLogprob
		sumNS += s.NoSpeechProb
	}
	n := float64(len(kept))
	return sumLP / n, sumNS / n, GzipRatio(finalText)
}
