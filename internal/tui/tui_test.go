package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pid1/voxthief/internal/events"
)

// step applies a message and returns the concrete Model back.
func step(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

// ready returns a sized model (viewport initialized).
func ready(t *testing.T) Model {
	t.Helper()
	m := New(Config{Input: "rtlsdr:146.520M", Model: "small.en-q8_0"}, nil)
	m = step(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	return m
}

func TestIdleView(t *testing.T) {
	m := ready(t)
	v := m.View().Content
	if !strings.Contains(v, "voxthief") || !strings.Contains(v, "rtlsdr:146.520M") {
		t.Errorf("header missing: %q", firstLines(v, 2))
	}
	if !strings.Contains(v, "listening…") {
		t.Errorf("idle log placeholder missing")
	}
	if !strings.Contains(v, "[q]uit") {
		t.Errorf("footer missing")
	}
}

func TestSquelchOpenStatus(t *testing.T) {
	m := ready(t)
	m = step(m, events.SquelchOpenedMsg{At: time.Now()})
	m = step(m, events.LevelMsg{RMSDBFS: -18, PeakDBFS: -3})
	v := m.View().Content
	if !strings.Contains(v, "SQUELCH OPEN") {
		t.Errorf("squelch-open indicator missing in status bar")
	}
	if !strings.Contains(v, "dBFS") {
		t.Errorf("level meter missing")
	}
}

func TestClipBadge(t *testing.T) {
	m := ready(t)
	m = step(m, events.LevelMsg{RMSDBFS: -5, PeakDBFS: 0.5}) // peak > -1 → CLIP
	if !strings.Contains(m.View().Content, "CLIP") {
		t.Errorf("CLIP badge missing when peak > -1 dBFS")
	}
}

func TestMessageAppended(t *testing.T) {
	m := ready(t)
	m = step(m, events.TransmissionMsg{Transmission: events.Transmission{
		StartedAt: time.Now(), Duration: 4200 * time.Millisecond,
		Text: "county ems respond to main", Status: "transcribed",
	}})
	v := m.View().Content
	if !strings.Contains(v, "county ems respond to main") {
		t.Errorf("appended transmission text missing")
	}
	if !strings.Contains(v, "rows:1") {
		t.Errorf("row counter not updated: %q", statusLine(v))
	}
}

func TestAlertMarkedRow(t *testing.T) {
	m := ready(t)
	m = step(m, events.TransmissionMsg{Transmission: events.Transmission{
		StartedAt: time.Now(), Duration: 2 * time.Second,
		Text: "unit 12 en route", Status: "transcribed",
		Alerted: true, AlertRules: []string{"callsign"},
	}})
	v := m.View().Content
	if !strings.Contains(v, "!") {
		t.Errorf("alert mark missing")
	}
	if !strings.Contains(v, "[callsign]") {
		t.Errorf("alert rule suffix missing: %q", v)
	}
	m = step(m, events.AlertMsg{Status: "sent", Rules: []string{"callsign"}})
	if !strings.Contains(m.View().Content, "alerts:1") {
		t.Errorf("alerts-sent counter not incremented")
	}
}

func TestFilteredToggle(t *testing.T) {
	m := ready(t)
	m = step(m, events.TransmissionMsg{Transmission: events.Transmission{
		StartedAt: time.Now(), Duration: time.Second, Status: "filtered", FilterReason: "vad_no_speech",
	}})
	// Hidden by default.
	if strings.Contains(m.renderLog(), "vad_no_speech") {
		t.Errorf("filtered row should be hidden by default")
	}
	// 'f' reveals filtered rows.
	m = step(m, keyPress("f"))
	if !strings.Contains(m.renderLog(), "vad_no_speech") {
		t.Errorf("'f' should reveal filtered rows")
	}
}

func TestNarrowTerminalWraps(t *testing.T) {
	// A narrow window: header, status bar, footer, and a long transcript must
	// all wrap rather than run off the page.
	m := New(Config{Input: "rtlsdr:162.550M", Model: "small.en-q8_0"}, nil)
	const w = 40
	m = step(m, tea.WindowSizeMsg{Width: w, Height: 24})
	m = step(m, events.SquelchOpenedMsg{At: time.Now()})
	m = step(m, events.LevelMsg{RMSDBFS: -18, PeakDBFS: -3})
	m = step(m, events.TransmissionMsg{Transmission: events.Transmission{
		StartedAt: time.Now(), Duration: 113800 * time.Millisecond,
		Text:   "Tuesday, mostly sunny, a chance of storms with slight storms in the morning, then showers likely with a chance of thunderstorms",
		Status: "transcribed",
	}})

	// The status bar wrapped to more than one line.
	if lineCount(m.renderStatus()) < 2 {
		t.Errorf("status bar should wrap at width %d, got 1 line: %q", w, m.renderStatus())
	}
	// No rendered line exceeds the terminal width (ANSI-stripped).
	for _, line := range strings.Split(m.View().Content, "\n") {
		if vw := ansi.StringWidth(line); vw > w {
			t.Errorf("line exceeds width %d (got %d): %q", w, vw, ansi.Strip(line))
		}
	}
}

func TestPauseWarn(t *testing.T) {
	m := ready(t)
	m = step(m, keyPress("p"))
	if !m.paused || !strings.Contains(m.View().Content, "paused") {
		t.Errorf("pause state/indicator missing")
	}
}

// keyPress builds a KeyPressMsg for a single rune key.
func keyPress(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}

func statusLine(v string) string {
	lines := strings.Split(v, "\n")
	for _, l := range lines {
		if strings.Contains(l, "rows:") {
			return l
		}
	}
	return ""
}
