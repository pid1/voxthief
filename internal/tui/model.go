package tui

import (
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/pid1/voxthief/internal/events"
)

// row is one rendered transmission in the log.
type row struct {
	startedAt    time.Time
	duration     time.Duration
	text         string
	status       string
	filterReason string
	avgLogprob   *float64
	alerted      bool
	alertRules   []string
}

// Config seeds the immutable header fields.
type Config struct {
	Input string // e.g. "rtlsdr:146.520M"
	Model string // e.g. "small.en-q8_0"
}

// Model is the Bubble Tea model (§9). All updates arrive as messages; there is
// no shared mutable state with the pipeline.
type Model struct {
	cfg    Config
	styles Styles

	vp     viewport.Model
	ready  bool
	width  int
	height int

	rows         []row
	showFiltered bool
	paused       bool
	lastText     string

	// status bar state
	squelchOpen  bool
	squelchSince time.Time
	rmsDBFS      float64
	peakDBFS     float64
	queueDepth   int
	drops        int64
	alertsSent   int
	transcribing bool

	// startup / transient state
	modelStatus string // model download line, empty when done
	warn        string // transient status-bar warning
	warnUntil   time.Time

	quitting bool
	onQuit   func() // graceful-drain hook invoked on quit
}

// New builds a Model. onQuit is invoked when the user quits so the caller can
// drain the pipeline before the program exits (§9 graceful drain).
func New(cfg Config, onQuit func()) Model {
	return Model{
		cfg:    cfg,
		styles: DefaultStyles(),
		onQuit: onQuit,
	}
}

// Init clears the screen and requests the terminal size up front — without the
// explicit RequestWindowSize the model stays unsized until the user resizes the
// window, leaving stale terminal content on screen. The alt screen is entered
// via View.AltScreen; the 1 Hz clock tick is also started here.
func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.ClearScreen, tea.RequestWindowSize, tick())
}

// tickMsg drives the status-bar clock at 1 Hz.
type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// visibleRows returns rows honoring the filtered-row toggle (§9).
func (m Model) visibleRows() []row {
	if m.showFiltered {
		return m.rows
	}
	out := make([]row, 0, len(m.rows))
	for _, r := range m.rows {
		if r.status == "filtered" {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Compile-time assurance the event types are wired (documents the contract).
var (
	_ = events.LevelMsg{}
	_ = events.TransmissionMsg{}
)
