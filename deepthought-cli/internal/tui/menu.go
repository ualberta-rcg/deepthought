package tui

import (
	"fmt"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type menuKind int

const (
	kindContinue menuKind = iota
	kindNew
	kindSettings
	kindQuit
)

type menuItem struct {
	num   int
	label string
	kind  menuKind
}

// MenuModel is a hand-rolled four-row menu (not bubbles/list — too small to
// warrant it). Plain struct, not a tea.Model.
type MenuModel struct {
	items        []menuItem
	sel          int
	width        int
	height       int
	healthy      bool
	healthReason string
}

// NewMenuModel builds the Continue / New Chat / Settings / Quit menu.
func NewMenuModel() MenuModel {
	return MenuModel{healthReason: "checking agentic model…", items: []menuItem{
		{1, "Continue", kindContinue},
		{2, "New Chat", kindNew},
		{3, "Settings", kindSettings},
		{4, "Quit", kindQuit},
	}}
}

func (m MenuModel) SetHealth(healthy bool, reason string) MenuModel {
	m.healthy, m.healthReason = healthy, reason
	if healthy {
		m.healthReason = ""
	}
	return m
}

func (m MenuModel) Init() tea.Cmd { return nil }

// Update returns the concrete MenuModel type. Navigation wraps; digits
// jump-select without opening.
func (m MenuModel) Update(msg tea.Msg) (MenuModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			m.sel = (m.sel - 1 + len(m.items)) % len(m.items)
		case "down", "j":
			m.sel = (m.sel + 1) % len(m.items)
		case "1", "2", "3", "4":
			if idx := int(msg.String()[0] - '1'); idx >= 0 && idx < len(m.items) {
				m.sel = idx
			}
		case "enter":
			return m.activate()
		case "q", "esc":
			return m, tea.Quit
		}
	}
	return m, nil
}

// activate acts on the selected row.
func (m MenuModel) activate() (MenuModel, tea.Cmd) {
	switch m.items[m.sel].kind {
	case kindContinue:
		if !m.healthy {
			return m, nil
		}
		return m, Goto(ScreenContinue)
	case kindNew:
		if !m.healthy {
			return m, nil
		}
		return m, Goto(ScreenChat)
	case kindSettings:
		return m, Goto(ScreenSettings)
	case kindQuit:
		return m, tea.Quit
	}
	return m, nil
}

// Resize stores geometry for View. The menu owns no size-sensitive components.
func (m MenuModel) Resize(w, h int) MenuModel {
	m.width, m.height = w, h
	return m
}

// View renders the menu as a small centered card (the full-terminal frame is
// reserved for Settings/Continue — the menu is just a launcher).
func (m MenuModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	rows := make([]string, 0, len(m.items))
	for i, it := range m.items {
		line := fmt.Sprintf("%d  %s", it.num, it.label)
		if !m.healthy && (it.kind == kindContinue || it.kind == kindNew) {
			line += "  [model unavailable]"
		}
		if i == m.sel {
			line = styleMenuSel.Render("▶ " + line)
		} else {
			line = styleMenuUnsel.Render("  " + line)
		}
		rows = append(rows, line)
	}
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	box := styleMenuBox.Render(body)
	full := lipgloss.JoinVertical(lipgloss.Center,
		box,
		"",
		styleMenuFoot.Render(m.healthReason),
		styleMenuFoot.Render("↑↓ move · enter open · q quit"),
	)
	return placeCenter(m.width, m.height, full)
}
