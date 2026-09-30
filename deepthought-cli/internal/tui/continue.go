package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/history"
	"deepthought-cli/internal/tui/kit"
)

// ContinueModel lists saved chats (from the ChatStore) and resumes the selected
// one into the chat screen — the "which chat do you want to rejoin" picker.
// Built on the kit List: `/` filters by title (fuzzy subsequence).
// Plain struct, not a tea.Model.
type ContinueModel struct {
	source    history.ChatStoreSource
	list      kit.List
	filtering bool
	toast     string
	width     int
	height    int
}

// NewContinueModel builds the chat lister backed by source.
func NewContinueModel(source history.ChatStoreSource) ContinueModel {
	return ContinueModel{source: source, list: kit.List{Empty: "No saved chats yet"}}
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
	items := make([]kit.Item, len(chats))
	for i, c := range chats {
		turns := "turns"
		if c.Incursions == 1 {
			turns = "turn"
		}
		items[i] = kit.Item{
			Label:  c.Title,
			Detail: fmt.Sprintf("%s · %d %s", c.UpdatedAt.Format("2006-01-02 15:04"), c.Incursions, turns),
			Value:  c.ID,
		}
	}
	m.list = kit.List{Items: items, Empty: "No saved chats yet"}
	m.filtering = false
}

// SetError surfaces a resume failure (or any error) on the Continue screen.
func (m *ContinueModel) SetError(msg string) { m.toast = "✗ " + msg }

// CapturingKeys reports whether typed letters belong to the filter.
func (m ContinueModel) CapturingKeys() bool { return m.filtering }

// Update returns the concrete ContinueModel type.
func (m ContinueModel) Update(msg tea.Msg) (ContinueModel, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.filtering {
		switch key.String() {
		case "esc":
			m.filtering = false
			m.list.SetFilter("")
		case "enter":
			m.filtering = false
		case "backspace":
			if f := []rune(m.list.Filter); len(f) > 0 {
				m.list.SetFilter(string(f[:len(f)-1]))
			}
		case "up", "ctrl+p":
			m.list.Move(-1)
		case "down", "ctrl+n":
			m.list.Move(1)
		default:
			if key.Text != "" {
				m.list.SetFilter(m.list.Filter + key.Text)
			}
		}
		return m, nil
	}
	switch key.String() {
	case "q", "esc", "left", "h":
		if m.list.Filter != "" {
			m.list.SetFilter("")
			return m, nil
		}
		return m, Back()
	case "up", "k", "ctrl+p":
		m.list.Move(-1)
	case "down", "j", "ctrl+n":
		m.list.Move(1)
	case kit.Mnemonics.Filter:
		m.filtering = true
	case "enter":
		it, ok := m.list.Selected()
		if !ok {
			return m, Back()
		}
		id, _ := it.Value.(string)
		return m, func() tea.Msg { return ResumeChatMsg{CollectID: id} }
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
	inner := m.width - 4
	var head []string
	if m.filtering || m.list.Filter != "" {
		cursor := ""
		if m.filtering {
			cursor = "█"
		}
		head = append(head, styleSettingsKey.Render("/ ")+m.list.Filter+cursor)
	}
	var tail []string
	if m.toast != "" {
		tail = append(tail, styleToast.Render(m.toast))
	}
	// frame border (2) + title (1) + keybar (1) + blank spacer (1)
	listH := max(1, m.height-5-len(head)-len(tail))
	rows := append([]string{""}, head...)
	rows = append(rows, m.list.Render(inner, listH, true)...)
	rows = append(rows, tail...)
	keys := []kit.Key{{Key: "↑↓", Help: "move"}, {Key: "enter", Help: "resume"}, {Key: "/", Help: "filter"}, {Key: "esc", Help: "back"}}
	if m.filtering {
		keys = []kit.Key{{Key: "type", Help: "filter"}, {Key: "enter", Help: "keep"}, {Key: "esc", Help: "clear"}}
	}
	return AppScreen(m.width, m.height, screenTitle("Continue"), strings.Join(rows, "\n"), kit.KeyBar(keys, inner))
}
