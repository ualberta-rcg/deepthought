package tui

import (
	"charm.land/bubbletea/v2"
	"strings"
)

type WorkspaceAction struct{ Kind, ID string }
type WorkspaceItem struct{ Label, Detail, Kind, ID string }
type WorkspaceModel struct {
	Title, Notice         string
	Items                 []WorkspaceItem
	Cursor, Width, Height int
}

func (m WorkspaceModel) Resize(w, h int) WorkspaceModel { m.Width, m.Height = w, h; return m }
func (m WorkspaceModel) Update(msg tea.Msg) (WorkspaceModel, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	n := len(m.Items)
	switch key.String() {
	case "esc":
		return m, Back()
	case "up", "k":
		if n > 0 {
			m.Cursor = (m.Cursor + n - 1) % n
		}
	case "down", "j", "tab":
		if n > 0 {
			m.Cursor = (m.Cursor + 1) % n
		}
	case "home":
		m.Cursor = 0
	case "end":
		if n > 0 {
			m.Cursor = n - 1
		}
	case "enter":
		if m.Cursor >= 0 && m.Cursor < n {
			i := m.Items[m.Cursor]
			return m, func() tea.Msg { return WorkspaceAction{Kind: i.Kind, ID: i.ID} }
		}
	}
	return m, nil
}
func (m WorkspaceModel) View() string {
	w, h := m.Width, m.Height
	if w < 4 || h < 4 {
		return ""
	}
	rows := []string{screenTitle(m.Title), clipLine(m.Notice, w-4), ""}
	visible := (h - 8) / 2
	if visible < 1 {
		visible = 1
	}
	start := 0
	if m.Cursor >= visible {
		start = m.Cursor - visible + 1
	}
	for i := start; i < len(m.Items) && i < start+visible; i++ {
		p := "  "
		if i == m.Cursor {
			p = "▶ "
		}
		rows = append(rows, clipLine(p+m.Items[i].Label, w-4), clipLine("  "+m.Items[i].Detail, w-4))
	}
	rows = append(rows, "", clipLine("↑↓ choose · enter open · esc back · Ctrl+P menu", w-4))
	return padBlock(strings.Join(rows, "\n"), w, h)
}

const InstallCommand = "curl -fsSL https://raw.githubusercontent.com/ualberta-rcg/deepthought/main/install.sh | bash"
