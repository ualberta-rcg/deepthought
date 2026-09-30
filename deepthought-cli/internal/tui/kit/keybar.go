package kit

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Key is one binding advertised in a key bar or help overlay.
type Key struct {
	Key, Help string
}

// Mnemonics are the single-letter keys every list uses for the same verbs.
var Mnemonics = struct {
	New, Delete, Edit, Refresh, Test, Filter, Keys string
}{"n", "d", "e", "r", "t", "/", "?"}

const keySep = "  "

// KeyBar packs keys left to right into at most w cells. When they don't all
// fit, it keeps as many as fit alongside a trailing ellipsis — never a
// dangling separator.
func KeyBar(keys []Key, w int) string {
	if w <= 0 {
		return ""
	}
	keyStyle := lipgloss.NewStyle().Foreground(theme.OnAccent).Background(theme.Accent).Bold(true)
	var cells []string
	total := 0
	for _, k := range keys {
		if k.Key == "" {
			continue
		}
		c := keyStyle.Render(" "+k.Key+" ") + muted().Render(" "+k.Help)
		if len(cells) > 0 {
			total += len(keySep)
		}
		total += lipgloss.Width(c)
		cells = append(cells, c)
	}
	if total <= w {
		return strings.Join(cells, muted().Render(keySep))
	}
	ell := G().Ellipsis
	tail := len(keySep) + lipgloss.Width(ell)
	var parts []string
	used := 0
	for _, c := range cells {
		need := lipgloss.Width(c)
		if len(parts) > 0 {
			need += len(keySep)
		}
		if used+need+tail > w {
			break
		}
		if len(parts) > 0 {
			parts = append(parts, muted().Render(keySep))
		}
		parts = append(parts, c)
		used += need
	}
	switch {
	case len(parts) > 0:
		parts = append(parts, muted().Render(keySep+ell))
	case lipgloss.Width(ell) <= w:
		parts = append(parts, muted().Render(ell))
	}
	return strings.Join(parts, "")
}
