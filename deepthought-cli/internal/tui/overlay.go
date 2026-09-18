package tui

import (
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Overlay is a transient centered picker that floats over the active screen
// (effort, model chooser, ...). The root holds a stack of Overlays so pickers
// can nest — e.g. the model chooser branches into effort, and confirming effort
// returns to the chooser. ←/→ (and ↑↓) move, enter commits, esc cancels.
type Overlay interface {
	Update(tea.Msg) (Overlay, tea.Cmd)
	View() string
	Resize(w, h int) Overlay
	Done() bool // true once committed or cancelled → root pops the stack
}

// ShowOverlayMsg asks the root to push an overlay onto the stack.
type ShowOverlayMsg struct{ O Overlay }

// ShowOverlay returns a Cmd that pushes o onto the overlay stack.
func ShowOverlay(o Overlay) tea.Cmd {
	return func() tea.Msg { return ShowOverlayMsg{O: o} }
}

// OpenEffortMsg asks the root to build + push the effort picker (it owns the
// settings handle, which the picker needs to apply the choice).
type OpenEffortMsg struct{}

// OpenModelChooserMsg asks the root to build + push the model chooser.
type OpenModelChooserMsg struct{}

// ShowEffortMsg asks the root to push effort on top of the current overlay
// (used by the model chooser's "e" branch so effort nests under the chooser and
// returns to it on confirm).
type ShowEffortMsg struct{}

// choiceOverlay is the generic left/right + up/down picker backing the effort
// (and any simple single-level) pickers. The model chooser has its own type
// because it branches into effort. commit returns a Cmd (usually one that emits
// a chosen-value msg) so the root can apply the choice in its own Update — this
// avoids value-receiver pitfalls where a closure could not safely mutate root
// state.
type choiceOverlay struct {
	title  string
	items  []overlayItem
	sel    int
	commit func(int) tea.Cmd
	width  int
	height int
	done   bool
}

type overlayItem struct {
	label string
	hint  string // optional dim right-aligned hint (e.g. "· current")
}

func (c choiceOverlay) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	n := len(c.items)
	switch kp.String() {
	case "left", "h", "up", "k":
		if n > 0 {
			c.sel = (c.sel - 1 + n) % n
		}
	case "right", "l", "down", "j":
		if n > 0 {
			c.sel = (c.sel + 1) % n
		}
	case "enter":
		c.done = true
		if c.commit != nil && c.sel >= 0 && c.sel < n {
			return c, c.commit(c.sel)
		}
	case "esc", "q":
		c.done = true
	}
	return c, nil
}

func (c choiceOverlay) Resize(w, h int) Overlay { c.width, c.height = w, h; return c }
func (c choiceOverlay) Done() bool              { return c.done }

func (c choiceOverlay) View() string {
	rows := make([]string, 0, len(c.items)+3)
	rows = append(rows, styleSettingsTitle.Render(c.title), "")
	if len(c.items) == 0 {
		rows = append(rows, styleSettingsFoot.Render("(nothing to choose)"))
	}
	for i, it := range c.items {
		line := it.label
		if it.hint != "" {
			line += "  " + styleSettingsFoot.Render(it.hint)
		}
		if i == c.sel {
			line = styleMenuSel.Render("▶ " + line)
		} else {
			line = styleMenuUnsel.Render("  " + line)
		}
		rows = append(rows, line)
	}
	rows = append(rows, "", styleMenuFoot.Render("←→ move · enter select · esc cancel"))
	card := styleMenuBox.Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
	return placeCenter(c.width, c.height, card)
}
