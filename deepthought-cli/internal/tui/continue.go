package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"annorax/internal/history"
)

// ContinueModel lists saved chats (from the ChatStore) and resumes the selected
// one into the chat screen — the "which chat do you want to rejoin" picker.
// Plain struct, not a tea.Model.
type ContinueModel struct {
	source history.ChatStoreSource
	chats  []history.ChatSummary
	sel    int
	toast  string
	width  int
	height int
}

// NewContinueModel builds the chat lister backed by source.
func NewContinueModel(source history.ChatStoreSource) ContinueModel {
	return ContinueModel{source: source}
}

func (m ContinueModel) Init() tea.Cmd { return nil }

// Refresh reloads the chat list. Called by the root on screen entry so
// newly-saved chats appear.
func (m *ContinueModel) Refresh() {
	if m.source == nil {
		return
	}
	chats, err := m.source().ListCollectives()
	if err != nil {
		m.toast = "✗ " + err.Error()
		chats = nil
	}
	m.chats = chats
	m.sel = 0
}

// SetError surfaces a resume failure (or any error) on the Continue screen.
func (m *ContinueModel) SetError(msg string) { m.toast = "✗ " + msg }

// Update returns the concrete ContinueModel type.
func (m ContinueModel) Update(msg tea.Msg) (ContinueModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "q", "esc", "left", "h":
			return m, Back()
		case "up", "k":
			if len(m.chats) > 0 {
				m.sel = (m.sel - 1 + len(m.chats)) % len(m.chats)
			}
		case "down", "j":
			if len(m.chats) > 0 {
				m.sel = (m.sel + 1) % len(m.chats)
			}
		case "enter":
			if len(m.chats) == 0 {
				return m, Back()
			}
			return m, func() tea.Msg { return ResumeChatMsg{CollectID: m.chats[m.sel].ID} }
		}
	}
	return m, nil
}

// Resize stores geometry.
func (m ContinueModel) Resize(w, h int) ContinueModel {
	m.width, m.height = w, h
	return m
}

// View renders the chat list inside the shared app frame.
func (m ContinueModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	rows := []string{""}
	if len(m.chats) == 0 {
		rows = append(rows, styleSettingsFoot.Render("(no saved chats)"))
	} else {
		rows = append(rows, m.header())
		for i, c := range m.chats {
			line := fmt.Sprintf("%-50s %s  %d turn",
				truncatePad(c.Title, 50),
				c.UpdatedAt.Format("2006-01-02 15:04"),
				c.Incursions)
			if i == m.sel {
				line = styleMenuSel.Render("▶ " + line)
			} else {
				line = styleMenuUnsel.Render("  " + line)
			}
			rows = append(rows, line)
		}
	}
	rows = append(rows, "")
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	keybar := KeyBar([]KeyHint{
		{"↑↓", "move"}, {"enter", "resume"}, {"esc", "back"},
	})
	frame := AppScreen(m.width, m.height, "Continue Chat", body, keybar)
	if m.toast != "" {
		frame = lipgloss.JoinVertical(lipgloss.Center, styleToast.Render(m.toast), frame)
	}
	return frame
}

// header renders a column header aligned with the list rows.
func (m ContinueModel) header() string {
	return styleSettingsKey.Render(fmt.Sprintf("%-50s %-16s %s", "prompt", "when", "turns"))
}

func truncatePad(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s + strings.Repeat(" ", n-len(r))
}
