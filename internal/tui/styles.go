// Package tui implements the Bubble Tea (v2) terminal UI: a live message log,
// a status bar, and a level meter (§9). Pipeline goroutines push events via
// program.Send; there is no shared mutable UI state. Styling is centralized
// here and respects the terminal background.
package tui

import "charm.land/lipgloss/v2"

// Styles bundles every lip gloss style the view uses (§9). Kept in one place so
// the look is consistent and easy to retune.
type Styles struct {
	Header      lipgloss.Style
	HeaderKey   lipgloss.Style
	Time        lipgloss.Style
	Duration    lipgloss.Style
	Text        lipgloss.Style
	DimText     lipgloss.Style
	Filtered    lipgloss.Style
	AlertMark   lipgloss.Style
	AlertRule   lipgloss.Style
	StatusBar   lipgloss.Style
	StatusOK    lipgloss.Style
	StatusWarn  lipgloss.Style
	SquelchOpen lipgloss.Style
	SquelchIdle lipgloss.Style
	Meter       lipgloss.Style
	Clip        lipgloss.Style
	Footer      lipgloss.Style
	Spinner     lipgloss.Style
}

// DefaultStyles returns the standard palette. Colors are ANSI/adaptive so they
// read on both light and dark terminals (§9).
func DefaultStyles() Styles {
	accent := lipgloss.Color("6") // cyan
	warn := lipgloss.Color("3")   // yellow
	danger := lipgloss.Color("1") // red
	dim := lipgloss.Color("8")    // bright black / gray
	ok := lipgloss.Color("2")     // green
	return Styles{
		Header:      lipgloss.NewStyle().Bold(true),
		HeaderKey:   lipgloss.NewStyle().Foreground(accent),
		Time:        lipgloss.NewStyle().Foreground(dim),
		Duration:    lipgloss.NewStyle().Foreground(dim),
		Text:        lipgloss.NewStyle(),
		DimText:     lipgloss.NewStyle().Foreground(dim),
		Filtered:    lipgloss.NewStyle().Foreground(dim).Italic(true),
		AlertMark:   lipgloss.NewStyle().Foreground(danger).Bold(true),
		AlertRule:   lipgloss.NewStyle().Foreground(dim),
		StatusBar:   lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		StatusOK:    lipgloss.NewStyle().Foreground(ok),
		StatusWarn:  lipgloss.NewStyle().Foreground(warn).Bold(true),
		SquelchOpen: lipgloss.NewStyle().Foreground(ok).Bold(true),
		SquelchIdle: lipgloss.NewStyle().Foreground(dim),
		Meter:       lipgloss.NewStyle().Foreground(accent),
		Clip:        lipgloss.NewStyle().Foreground(danger).Bold(true),
		Footer:      lipgloss.NewStyle().Foreground(dim),
		Spinner:     lipgloss.NewStyle().Foreground(accent),
	}
}
