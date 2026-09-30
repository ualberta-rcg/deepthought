package kit

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Dialog is the one centered-overlay chrome: title (truncated, never
// wrapped), optional title info (dropped when it can't fit), body rows, and a
// packed help line. esc always cancels; callers own the key handling.
type Dialog struct {
	Title string
	Info  string
	Body  []string
	Help  []Key
	// Width is the preferred total width (0 = fit the body, min 30).
	Width int
}

// Render draws the dialog clamped to the area (areaW × areaH) so it fits
// small terminals: the body is clipped with an ellipsis row before the frame
// ever overflows.
func (d Dialog) Render(areaW, areaH int) string {
	w := d.Width
	if w == 0 {
		w = 30
		for _, ln := range d.Body {
			w = max(w, lipgloss.Width(ln)+4)
		}
		w = max(w, lipgloss.Width(d.Title)+6)
	}
	w = max(8, min(w, areaW))
	inner := w - 4
	body := append([]string(nil), d.Body...)
	help := ""
	if len(d.Help) > 0 {
		help = KeyBar(d.Help, inner)
	}
	maxBody := areaH - 2 - 1 // border + title row
	if help != "" {
		maxBody -= 2 // blank + help
	}
	if maxBody < 1 {
		maxBody = 1
	}
	if len(body) > maxBody {
		body = append(body[:maxBody-1], muted().Render(G().Ellipsis))
	}

	title := accent().Bold(true).Render(Fit(d.Title, min(lipgloss.Width(d.Title), inner)))
	if d.Info != "" {
		info := muted().Render(d.Info)
		if lipgloss.Width(title)+1+lipgloss.Width(info) <= inner {
			title += strings.Repeat(" ", inner-lipgloss.Width(title)-lipgloss.Width(info)) + info
		}
	}
	lines := []string{title}
	lines = append(lines, body...)
	if help != "" {
		lines = append(lines, "", help)
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Accent).
		Padding(0, 1).
		Width(w)
	if ASCII() {
		box = box.Border(lipgloss.ASCIIBorder())
	}
	return box.Render(strings.Join(Block(lines, inner, 0), "\n"))
}
