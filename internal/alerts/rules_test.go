package alerts

import (
	"strings"
	"testing"

	"github.com/pid1/voxthief/internal/config"
)

func TestCompileErrors(t *testing.T) {
	_, err := Compile([]config.RuleConfig{{Name: "bad", Pattern: "("}})
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("expected compile error naming rule, got %v", err)
	}
}

func TestMatchCaseSensitivity(t *testing.T) {
	rules, err := Compile([]config.RuleConfig{
		{Name: "ci", Pattern: "callsign"},
		{Name: "cs", Pattern: "K5ABC", CaseSensitive: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Case-insensitive default matches any case.
	if _, _, ok := MatchAll(rules[:1], "the CALLSIGN is"); !ok {
		t.Error("case-insensitive rule should match uppercase")
	}
	// Case-sensitive rule only matches exact case.
	if _, _, ok := MatchAll(rules[1:], "k5abc"); ok {
		t.Error("case-sensitive rule should not match lowercase")
	}
	if _, _, ok := MatchAll(rules[1:], "K5ABC"); !ok {
		t.Error("case-sensitive rule should match exact case")
	}
}

func TestWordBoundary(t *testing.T) {
	rules, _ := Compile([]config.RuleConfig{{Name: "cs", Pattern: `\bK5ABC\b`}})
	if _, _, ok := MatchAll(rules, "unit K5ABC responding"); !ok {
		t.Error("expected boundary match")
	}
	if _, _, ok := MatchAll(rules, "XK5ABCY"); ok {
		t.Error("boundary should prevent substring match")
	}
}

func TestPlainKeyword(t *testing.T) {
	rules, _ := Compile([]config.RuleConfig{{Name: "street", Pattern: "main street"}})
	if _, _, ok := MatchAll(rules, "respond to Main Street"); !ok {
		t.Error("plain keyword should match")
	}
}

func TestMergeSingleNotification(t *testing.T) {
	rules, _ := Compile([]config.RuleConfig{
		{Name: "callsign", Pattern: "K5ABC", Priority: 0, Sound: "pushover"},
		{Name: "street", Pattern: "main", Priority: 2, Sound: "siren", Retry: 60, Expire: 3600},
		{Name: "agency", Pattern: "county", Priority: 1, Sound: "bugle"},
	})
	m, matched, ok := MatchAll(rules, "K5ABC to county on main")
	if !ok {
		t.Fatal("expected match")
	}
	if len(matched) != 3 || len(m.Rules) != 3 {
		t.Fatalf("expected 3 matched rules, got %d", len(m.Rules))
	}
	// Priority = max = 2; sound from the highest-priority (street).
	if m.Priority != 2 {
		t.Errorf("priority = %d, want 2", m.Priority)
	}
	if m.Sound != "siren" {
		t.Errorf("sound = %q, want siren (highest priority)", m.Sound)
	}
	if m.Retry != 60 || m.Expire != 3600 {
		t.Errorf("retry/expire not carried from delivering rule: %d/%d", m.Retry, m.Expire)
	}
	// Title lists rule names in config order.
	if m.Title != "voxthief: callsign, street, agency" {
		t.Errorf("title = %q", m.Title)
	}
}

func TestPriorityTieBreakByConfigOrder(t *testing.T) {
	rules, _ := Compile([]config.RuleConfig{
		{Name: "first", Pattern: "a", Priority: 1, Sound: "one"},
		{Name: "second", Pattern: "b", Priority: 1, Sound: "two"},
	})
	m, _, ok := MatchAll(rules, "a b")
	if !ok {
		t.Fatal("expected match")
	}
	if m.Sound != "one" {
		t.Errorf("tie should break to first config rule, got sound %q", m.Sound)
	}
}

func TestTitleTruncation(t *testing.T) {
	long := strings.Repeat("x", 300)
	rules, _ := Compile([]config.RuleConfig{{Name: long, Pattern: "hit"}})
	m, _, _ := MatchAll(rules, "hit")
	if len([]rune(m.Title)) > titleMax {
		t.Errorf("title length %d exceeds %d", len([]rune(m.Title)), titleMax)
	}
}
