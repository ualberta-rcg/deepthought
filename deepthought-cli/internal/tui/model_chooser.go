package tui

import (
	"fmt"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/unimatrix"
)

// ModelChosenMsg carries the model id selected in the chooser. The root applies
// it (SetAgenticModel + status update) in its own Update.
type ModelChosenMsg struct{ ID string }

// modelChooser is the running-model picker (F3). It is distinct
// from Settings › Models (which edits model definitions): this only switches
// which model drives the chat. Pressing "e" branches into the effort picker,
// which nests under the chooser and returns to it on confirm.
type modelChooser struct {
	models []unimatrix.Model
	sel    int
	width  int
	height int
	done   bool
}

// NewModelChooser builds the chooser. currentID pre-selects the running model;
// choosing emits modelChosenMsg for the root to apply.
func NewModelChooser(models []unimatrix.Model, currentID string) Overlay {
	sel := 0
	for i, mm := range models {
		if mm.ID == currentID {
			sel = i
		}
	}
	return modelChooser{models: models, sel: sel}
}

func (m modelChooser) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	n := len(m.models)
	switch kp.String() {
	case "up", "k", "left", "h":
		if n > 0 {
			m.sel = (m.sel - 1 + n) % n
		}
	case "down", "j", "right", "l":
		if n > 0 {
			m.sel = (m.sel + 1) % n
		}
	case "enter":
		m.done = true
		if m.sel >= 0 && m.sel < n {
			id := m.models[m.sel].ID
			return m, func() tea.Msg { return ModelChosenMsg{ID: id} }
		}
	case "e":
		// Branch to effort; the root pushes it on top of this chooser.
		return m, func() tea.Msg { return ShowEffortMsg{} }
	case "esc", "q":
		m.done = true
	}
	return m, nil
}

func (m modelChooser) Resize(w, h int) Overlay { m.width, m.height = w, h; return m }
func (m modelChooser) Done() bool              { return m.done }

func (m modelChooser) View() string {
	rows := []string{styleSettingsTitle.Render("Model"), ""}
	if len(m.models) == 0 {
		rows = append(rows, styleSettingsFoot.Render("(no agentic models — add one in Settings)"))
	}
	for i, mm := range m.models {
		label := mm.Label
		if label == "" {
			label = mm.ID
		}
		line := fmt.Sprintf("%s  %s", truncatePad(label, 28), styleSettingsFoot.Render(mm.Provider))
		if i == m.sel {
			line = styleMenuSel.Render("▶ " + line)
		} else {
			line = styleMenuUnsel.Render("  " + line)
		}
		rows = append(rows, line)
	}
	rows = append(rows, "", styleMenuFoot.Render("↑↓ move · enter select · e effort · esc cancel"))
	card := styleMenuBox.Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
	return placeCenter(m.width, m.height, card)
}
