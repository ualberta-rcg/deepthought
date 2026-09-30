// Package kit is DeepThought's shared TUI building-block set: glyphs with an
// ASCII fallback, fuzzy matching, and the Panel / List / KeyBar / Dialog
// renderers every screen composes. Renderers are pure functions of their
// inputs and a width (and height where it matters) and always return
// exactly-sized blocks, so screens can join them without re-measuring.
//
// Rules the kit enforces (see the Part 7 design standard):
//   - never SGR dim/faint: muted text is a colour, dim is unreliable over SSH
//   - every status glyph has an ASCII form (DEEPTHOUGHT_ASCII=1, TERM=dumb/linux)
//   - titles truncate, never wrap; optional info is dropped when it can't fit
//   - help lines pack greedily and end with "…", never a dangling separator
package kit

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Theme holds the role colours. Defaults mirror internal/tui/styles.go.
type Theme struct {
	Accent   color.Color // focus, selection, titles
	Muted    color.Color // hints, footnotes, unfocused borders
	Text     color.Color
	OnAccent color.Color // text on an Accent background
	Success  color.Color
	Warning  color.Color
	Danger   color.Color
	Match    color.Color // fuzzy-match highlight
}

var theme = Theme{
	Accent:   lipgloss.Color("#7DD3FC"),
	Muted:    lipgloss.Color("244"),
	Text:     lipgloss.Color("252"),
	OnAccent: lipgloss.Color("#000000"),
	Success:  lipgloss.Color("#86EFAC"),
	Warning:  lipgloss.Color("#FCD34D"),
	Danger:   lipgloss.Color("#FCA5A5"),
	Match:    lipgloss.Color("#FCD34D"),
}

// SetTheme replaces the role colours (the tui package calls it once so the
// palette has a single source).
func SetTheme(t Theme) { theme = t }

// CurrentTheme returns the active role colours.
func CurrentTheme() Theme { return theme }

func muted() lipgloss.Style  { return lipgloss.NewStyle().Foreground(theme.Muted) }
func accent() lipgloss.Style { return lipgloss.NewStyle().Foreground(theme.Accent) }
func text() lipgloss.Style   { return lipgloss.NewStyle().Foreground(theme.Text) }
