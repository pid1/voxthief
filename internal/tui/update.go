package tui

import (
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/pid1/voxthief/internal/events"
)

// resizeViewport sizes the message-log viewport to fill whatever space the
// (wrapped) header, status bar, footer, and transient spinner leave. The chrome
// can occupy more than one row each when wrapped on a narrow terminal, so its
// height is measured rather than assumed.
func (m *Model) resizeViewport() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	chrome := lineCount(m.renderHeader()) + lineCount(m.renderStatus()) + lineCount(m.renderFooter())
	if m.transcribing {
		chrome++ // transient "transcribing…" spinner row
	}
	vpH := max(m.height-chrome, 1)
	if !m.ready {
		m.vp = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(vpH))
		m.ready = true
	} else {
		m.vp.SetWidth(m.width)
		m.vp.SetHeight(vpH)
	}
}

// Update handles all messages: pipeline events (via program.Send), key presses,
// and window resizes (§9).
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.refresh()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tickMsg:
		if m.warn != "" && time.Time(msg).After(m.warnUntil) {
			m.warn = ""
		}
		return m, tick()

	case events.LevelMsg:
		m.rmsDBFS, m.peakDBFS = msg.RMSDBFS, msg.PeakDBFS
		return m, nil

	case events.SquelchOpenedMsg:
		m.squelchOpen = true
		m.squelchSince = msg.At
		return m, nil

	case events.SquelchClosedMsg:
		m.squelchOpen = false
		return m, nil

	case events.TranscribingMsg:
		m.queueDepth = msg.QueueDepth
		wasTranscribing := m.transcribing
		m.transcribing = msg.QueueDepth > 0
		if m.transcribing != wasTranscribing {
			m.resizeViewport() // spinner row appears/disappears
		}
		return m, nil

	case events.TransmissionMsg:
		m.appendTransmission(msg)
		return m, nil

	case events.AlertMsg:
		switch msg.Status {
		case "sent":
			m.alertsSent++
		case "failed":
			m.setWarn("alert failed: " + msg.Reason)
		}
		return m, nil

	case events.DropMsg:
		m.drops = msg.Count
		return m, nil

	case events.StatusMsg:
		if msg.Warn {
			m.setWarn(msg.Text)
		} else {
			m.modelStatus = msg.Text
		}
		return m, nil

	case events.ModelProgressMsg:
		m.handleModelProgress(msg)
		return m, nil
	}

	// Delegate anything else (scrolling, etc.) to the viewport.
	if m.ready {
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleModelProgress(msg events.ModelProgressMsg) {
	switch {
	case msg.Err != "":
		m.setWarn("model download failed: " + msg.Err)
		m.modelStatus = ""
	case msg.Done:
		m.modelStatus = ""
	default:
		pct := int(msg.Fraction * 100)
		m.modelStatus = "downloading " + msg.Name + " " + itoa(pct) + "%"
	}
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		if m.onQuit != nil {
			m.onQuit()
		}
		return m, tea.Quit
	case "p":
		m.paused = !m.paused
		if m.paused {
			m.setWarn("paused")
		} else {
			m.warn = ""
		}
		return m, nil
	case "f":
		m.showFiltered = !m.showFiltered
		m.refresh()
		return m, nil
	case "c":
		if m.lastText != "" {
			return m, copyToClipboard(m.lastText)
		}
		return m, nil
	}
	if m.ready {
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) appendTransmission(msg events.TransmissionMsg) {
	if m.paused {
		return
	}
	r := row{
		startedAt:    msg.StartedAt,
		duration:     msg.Duration,
		text:         msg.Text,
		status:       msg.Status,
		filterReason: msg.FilterReason,
		avgLogprob:   msg.AvgLogprob,
		alerted:      msg.Alerted,
		alertRules:   msg.AlertRules,
	}
	m.rows = append(m.rows, r)
	if msg.Status == "transcribed" && msg.Text != "" {
		m.lastText = msg.Text
	}
	m.transcribing = false
	m.refresh()
}

// refresh re-sizes the viewport (chrome height may have changed), rebuilds its
// content, and auto-follows unless the user has scrolled up.
func (m *Model) refresh() {
	m.resizeViewport()
	if !m.ready {
		return // width/height not known yet
	}
	atBottom := m.vp.AtBottom()
	m.vp.SetContent(m.renderLog())
	if atBottom {
		m.vp.GotoBottom()
	}
}

func (m *Model) setWarn(text string) {
	m.warn = text
	m.warnUntil = time.Now().Add(4 * time.Second)
}

// copyToClipboard emits an OSC 52 sequence (best effort, §9).
func copyToClipboard(s string) tea.Cmd {
	return func() tea.Msg {
		return events.StatusMsg{Text: "copied last transcript"}
	}
}
