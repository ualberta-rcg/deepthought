package kit

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Panel is a bordered card: title in the top border on the left, optional
// status on the right (dropped when it can't fit with a 1-cell gap), body
// rows, and an optional muted footnote as the last inner row.
type Panel struct {
	Title    string
	Status   string
	Body     []string
	Footnote string
	Focused  bool
}

// Render draws the panel exactly w wide and h tall (h <= 0: as tall as the
// content). The footnote is the first thing dropped when height is short.
func (p Panel) Render(w, h int) string {
	if w < 4 {
		return ""
	}
	g := G()
	border := muted()
	if p.Focused {
		border = accent().Bold(true)
	}
	inner := w - 2
	body := append([]string(nil), p.Body...)
	rows := len(body)
	if p.Footnote != "" {
		rows++
	}
	if h <= 0 {
		h = rows + 2
	}
	avail := max(0, h-2)
	foot := p.Footnote
	if foot != "" && len(body)+1 > avail {
		foot = ""
	}
	bodyRows := avail
	if foot != "" {
		bodyRows--
	}
	if len(body) > bodyRows && bodyRows > 0 {
		body = body[:bodyRows]
		body[bodyRows-1] = muted().Render(g.Ellipsis)
	}
	var lines []string
	if bodyRows > 0 {
		lines = Block(body, inner, bodyRows)
	}
	if foot != "" {
		lines = append(lines, Fit(muted().Render(foot), inner))
	}

	out := make([]string, 0, h)
	out = append(out, p.top(inner, border))
	for _, ln := range lines {
		out = append(out, border.Render(g.V)+ln+border.Render(g.V))
	}
	out = append(out, border.Render(g.BL+strings.Repeat(g.H, inner)+g.BR))
	return strings.Join(out, "\n")
}

// top renders "╭─ Title ───── status ─╮" within inner cells between corners.
func (p Panel) top(inner int, border lipgloss.Style) string {
	g := G()
	titleStyle := text().Bold(true)
	if p.Focused {
		titleStyle = accent().Bold(true)
	}
	title := ""
	if p.Title != "" && inner >= 5 {
		title = " " + Fit(p.Title, min(lipgloss.Width(p.Title), inner-4)) + " "
	}
	status := ""
	if p.Status != "" {
		s := " " + p.Status + " "
		if lipgloss.Width(title)+lipgloss.Width(s)+3 <= inner {
			status = s
		}
	}
	fill := inner - 1 - lipgloss.Width(title) - lipgloss.Width(status)
	if status != "" {
		fill--
	}
	line := border.Render(g.TL+g.H) + titleStyle.Render(title) + border.Render(strings.Repeat(g.H, max(0, fill)))
	if status != "" {
		line += muted().Render(status) + border.Render(g.H)
	}
	return line + border.Render(g.TR)
}
