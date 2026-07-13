package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// View renders the full UI. The alt screen is requested via View.AltScreen
// (the Bubble Tea v2 way; there is no WithAltScreen option).
func (m Model) View() tea.View {
	if !m.ready {
		v := tea.NewView("starting voxthief…")
		v.AltScreen = true
		return v
	}
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteByte('\n')
	b.WriteString(m.vp.View())
	b.WriteByte('\n')
	if m.transcribing {
		b.WriteString(m.styles.Spinner.Render(fmt.Sprintf("▍ transcribing… queue %d", m.queueDepth)))
		b.WriteByte('\n')
	}
	b.WriteString(m.renderStatus())
	b.WriteByte('\n')
	b.WriteString(m.renderFooter())

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m Model) renderHeader() string {
	s := m.styles
	parts := []string{
		s.Header.Render("voxthief"),
		s.HeaderKey.Render("in: ") + m.cfg.Input,
		s.HeaderKey.Render("model: ") + m.cfg.Model,
	}
	line := strings.Join(parts, " ── ")
	if m.modelStatus != "" {
		line += "  " + s.StatusWarn.Render(m.modelStatus)
	}
	return m.wrap(line)
}

// wrap word-wraps s to the current width, preserving ANSI styling. Width 0
// (not yet sized) returns s unchanged.
func (m Model) wrap(s string) string {
	if m.width <= 0 {
		return s
	}
	return ansi.Wordwrap(s, m.width, "-")
}

// lineCount returns the number of terminal rows a rendered block occupies.
func lineCount(s string) int { return strings.Count(s, "\n") + 1 }

// renderLog builds the scrollable message-log content (§9).
func (m Model) renderLog() string {
	rows := m.visibleRows()
	if len(rows) == 0 {
		return m.styles.DimText.Render("listening…")
	}
	width := m.vp.Width()
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		line := m.renderRow(r)
		if width > 0 {
			// Word-wrap to the viewport width so long transcripts don't run off
			// the page; ansi.Wordwrap preserves the styling escape codes.
			line = ansi.Wordwrap(line, width, "-")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// renderRow renders one transmission line (§9): local time, duration, text;
// dim when avg_logprob < -0.7; alert rows get an accent "!" prefix and a dim
// "[rule]" suffix; filtered rows render as a dim marker.
func (m Model) renderRow(r row) string {
	s := m.styles
	ts := s.Time.Render(r.startedAt.Local().Format("15:04:05"))
	dur := s.Duration.Render(fmt.Sprintf("(%.1fs)", r.duration.Seconds()))

	if r.status == "filtered" {
		return fmt.Sprintf("%s  %s", ts, s.Filtered.Render("── filtered: "+r.filterReason+" ──"))
	}

	mark := "  "
	if r.alerted {
		mark = s.AlertMark.Render("! ")
	}
	textStyle := s.Text
	if r.avgLogprob != nil && *r.avgLogprob < -0.7 {
		textStyle = s.DimText
	}
	line := fmt.Sprintf("%s %s %s%s", ts, dur, mark, textStyle.Render(r.text))
	if r.alerted && len(r.alertRules) > 0 {
		line += " " + s.AlertRule.Render("["+strings.Join(r.alertRules, ",")+"]")
	}
	return line
}

// renderStatus renders the status bar (§9).
func (m Model) renderStatus() string {
	s := m.styles
	var squelch string
	if m.squelchOpen {
		elapsed := time.Since(m.squelchSince).Truncate(time.Second)
		squelch = s.SquelchOpen.Render(fmt.Sprintf("● SQUELCH OPEN %s", fmtDur(elapsed)))
	} else {
		squelch = s.SquelchIdle.Render("○ idle")
	}

	meter := s.Meter.Render(fmt.Sprintf("%s %.0f dBFS", bars(m.rmsDBFS), m.rmsDBFS))
	clip := ""
	if m.peakDBFS > -1 {
		clip = " " + s.Clip.Render("CLIP")
	}

	fields := []string{
		squelch,
		meter + clip,
		fmt.Sprintf("q:%d", m.queueDepth),
		fmt.Sprintf("drops:%d", m.drops),
		fmt.Sprintf("alerts:%d", m.alertsSent),
		fmt.Sprintf("rows:%d", len(m.rows)),
		time.Now().Format("15:04:05"),
	}
	bar := s.StatusBar.Render(strings.Join(fields, " │ "))
	if m.warn != "" {
		bar += "  " + s.StatusWarn.Render(m.warn)
	}
	return m.wrap(bar)
}

func (m Model) renderFooter() string {
	return m.wrap(m.styles.Footer.Render("[q]uit [p]ause [f]iltered [c]opy last [↑/↓] scroll"))
}

// bars renders a tiny 4-cell level meter from an RMS dBFS value in [-60, 0].
func bars(dbfs float64) string {
	glyphs := []rune("▁▂▃▄▅▆▇█")
	norm := (dbfs + 60) / 60 // 0..1
	if norm < 0 {
		norm = 0
	}
	if norm > 1 {
		norm = 1
	}
	n := 4
	var b strings.Builder
	for i := 0; i < n; i++ {
		level := norm*float64(n) - float64(i)
		idx := int(level * float64(len(glyphs)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(glyphs) {
			idx = len(glyphs) - 1
		}
		b.WriteRune(glyphs[idx])
	}
	return b.String()
}

func fmtDur(d time.Duration) string {
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d", m, s)
}

func itoa(n int) string { return strconv.Itoa(n) }
