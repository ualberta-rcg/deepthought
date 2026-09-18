package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// GridModel is the /vortex context-manifest viewer.
type GridModel struct {
	width, height int
	lines         []string
}

func NewGridModel() GridModel {
	return GridModel{lines: []string{
		"Context Grid",
		"",
		"No manifest has been emitted for this attached view yet.",
		"Each headless turn records included IDs, residency states, token estimates, and reasons.",
	}}
}

func (m GridModel) Init() tea.Cmd { return nil }
func (m GridModel) Resize(w, h int) GridModel {
	m.width, m.height = w, h
	return m
}
func (m GridModel) Update(msg tea.Msg) (GridModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc", "left", "h", "q":
			return m, Back()
		}
	}
	return m, nil
}
func (m GridModel) View() string {
	body := strings.Join(m.lines, "\n")
	return AppScreen(m.width, m.height, "DeepThought › Grid", body,
		KeyBar([]KeyHint{{Key: "esc", Label: "back"}}))
}
