// Package alerts implements keyword alerting over transcribed text: RE2 rule
// matching, the Pushover client, and the async dispatcher (§7). Alert delivery
// never blocks or slows the capture/transcription pipeline (§2.11, §3.2).
package alerts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pid1/voxthief/internal/config"
)

// Rule is a compiled alert rule (§7.1).
type Rule struct {
	Name      string
	re        *regexp.Regexp
	Priority  int
	Sound     string
	CooldownS int
	Retry     int
	Expire    int
}

// Compile compiles config rules into matchers. Patterns are RE2 (no
// catastrophic backtracking); matching is case-insensitive unless the rule sets
// case_sensitive (§7.1).
func Compile(rules []config.RuleConfig) ([]Rule, error) {
	out := make([]Rule, 0, len(rules))
	for _, rc := range rules {
		pattern := rc.Pattern
		if !rc.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", rc.Name, err)
		}
		out = append(out, Rule{
			Name:      rc.Name,
			re:        re,
			Priority:  rc.Priority,
			Sound:     rc.Sound,
			CooldownS: rc.CooldownS,
			Retry:     rc.Retry,
			Expire:    rc.Expire,
		})
	}
	return out, nil
}

// Match is the merged result of evaluating all rules against one transmission
// (§7.1). One notification per transmission regardless of how many rules match.
type Match struct {
	Rules    []string // matched rule names, in config order
	Priority int      // max priority across matched rules
	Sound    string   // sound of the highest-priority matched rule (config order breaks ties)
	Title    string   // "voxthief: {rule[, rule…]}", truncated to 250
	Retry    int      // retry of the delivering (highest-priority) rule, for priority 2
	Expire   int      // expire of the delivering rule, for priority 2
}

// titleMax and messageMax bound the Pushover fields (§7.2).
const (
	titleMax   = 250
	messageMax = 1000
)

// MatchAll evaluates rules against text and returns the merged Match, the
// matched rules (for cooldown bookkeeping), and whether anything matched.
func MatchAll(rules []Rule, text string) (Match, []Rule, bool) {
	var matched []Rule
	var names []string
	for _, r := range rules {
		if r.re.MatchString(text) {
			matched = append(matched, r)
			names = append(names, r.Name)
		}
	}
	if len(matched) == 0 {
		return Match{}, nil, false
	}

	// Priority = max; the delivering rule is the highest-priority matched rule,
	// with config order breaking ties. Its sound/retry/expire are used (§7.1).
	deliver := matched[0]
	for _, r := range matched[1:] {
		if r.Priority > deliver.Priority {
			deliver = r
		}
	}

	m := Match{
		Rules:    names,
		Priority: deliver.Priority,
		Sound:    deliver.Sound,
		Title:    truncate("voxthief: "+strings.Join(names, ", "), titleMax),
		Retry:    deliver.Retry,
		Expire:   deliver.Expire,
	}
	return m, matched, true
}

// truncate shortens s to max runes, appending an ellipsis when cut.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}
