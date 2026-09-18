package tui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"annorax/internal/history"
)

// Rough $/MTok rates used only for a glance estimate on the Stats page
// (not billing). Tuned for a mid-tier open-weight gateway.
const (
	estCostPerMIn  = 0.15
	estCostPerMOut = 0.60
)

// StatsModel is the F8 Stats page: this session's token use, last-turn context
// size, and per-model lifetime totals. Plain struct, not a tea.Model.
type StatsModel struct {
	sessionIn, sessionOut, lastContext int
	cycles, messages                   int
	lifetime                           func() map[string]history.Cost
	started                            time.Time
	clock                              time.Time
	modelLabel                         string
	vp                                 viewport.Model
	width, height                      int
}

// NewStatsModel builds the Stats page.
func NewStatsModel(lifetime func() map[string]history.Cost) StatsModel {
	return StatsModel{
		lifetime: lifetime,
		started:  time.Now(),
		vp:       viewport.New(),
	}
}

func (m StatsModel) Init() tea.Cmd { return nil }

func (m StatsModel) Resize(w, h int) StatsModel {
	m.width, m.height = w, h
	m.vp.SetWidth(w - 4)
	bodyH := h - 2 - 2
	if bodyH < 1 {
		bodyH = 1
	}
	m.vp.SetHeight(bodyH)
	return m
}

// SetSession refreshes live session totals from the chat.
func (m StatsModel) SetSession(in, out, lastContext int) StatsModel {
	m.sessionIn, m.sessionOut, m.lastContext = in, out, lastContext
	return m
}

// SetActivity refreshes cycle / message counters.
func (m StatsModel) SetActivity(cycles, messages int) StatsModel {
	m.cycles, m.messages = cycles, messages
	return m
}

// SetClock stamps the live clock.
func (m StatsModel) SetClock(t time.Time) StatsModel { m.clock = t; return m }

// SetModelLabel sets the active model name for the header.
func (m StatsModel) SetModelLabel(s string) StatsModel { m.modelLabel = s; return m }

func (m StatsModel) Update(msg tea.Msg) (StatsModel, tea.Cmd) {
	if kp, ok := msg.(tea.KeyPressMsg); ok {
		switch kp.String() {
		case "esc", "left", "h", "q":
			return m, Back()
		case "up", "k", "pgup", "down", "j", "pgdown", "home", "end", "g", "G":
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m StatsModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	est := estSessionCost(m.sessionIn, m.sessionOut)
	rows := []string{
		styleSettingsTitle.Render("This session"),
		kv("model", orDefault(m.modelLabel, "—")),
		kv("elapsed", formatElapsed(time.Since(m.started))),
		kv("input", fmt.Sprintf("%s tokens", formatTokens(m.sessionIn))),
		kv("output", fmt.Sprintf("%s tokens", formatTokens(m.sessionOut))),
		kv("total", fmt.Sprintf("%s tokens", formatTokens(m.sessionIn+m.sessionOut))),
		kv("context", fmt.Sprintf("%s (last turn window fill)", formatTokens(m.lastContext))),
		kv("cycles", fmt.Sprintf("%d LLM rounds", m.cycles)),
		kv("messages", fmt.Sprintf("%d (user + assistant)", m.messages)),
		kv("est. cost", est),
		"",
		styleSettingsTitle.Render("Lifetime (all chats)"),
	}
	var usage map[string]history.Cost
	if m.lifetime != nil {
		usage = m.lifetime()
	}
	if len(usage) == 0 {
		rows = append(rows, styleSettingsFoot.Render("  (no recorded usage yet)"))
	} else {
		for id, c := range usage {
			rows = append(rows, fmt.Sprintf("  %s  %s",
				truncatePad(id, 28),
				styleSettingsFoot.Render(fmt.Sprintf("in %s · out %s",
					formatTokens(c.InputTokens), formatTokens(c.OutputTokens)))))
		}
	}
	rows = append(rows, "",
		styleSettingsFoot.Render("Note: session input sums every tool-loop cycle's prompt_tokens"),
		styleSettingsFoot.Render("(billing). Context is the most recent window fill only."),
		styleSettingsFoot.Render(fmt.Sprintf("Est. cost uses ≈$%.2f/M in · $%.2f/M out (not a bill).", estCostPerMIn, estCostPerMOut)),
	)

	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	m.vp.SetContent(body)
	keybar := KeyBar([]KeyHint{
		{Key: "↑/↓", Label: "scroll"},
		{Key: "esc", Label: "back"},
	})
	return AppScreenScroll(m.width, m.height, "Annorax › Stats", m.vp.View(), m.vp.Height(), keybar)
}

func estSessionCost(in, out int) string {
	if in+out == 0 {
		return "—"
	}
	usd := (float64(in)/1e6)*estCostPerMIn + (float64(out)/1e6)*estCostPerMOut
	if usd < 0.01 {
		return fmt.Sprintf("~$%.4f", usd)
	}
	return fmt.Sprintf("~$%.2f", usd)
}
