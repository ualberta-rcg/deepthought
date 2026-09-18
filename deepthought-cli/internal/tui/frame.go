package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// KeyHint is one entry in the bottom keybinding bar: a key + what it does.
type KeyHint struct {
	Key   string
	Label string
}

// KeyBar renders a lazygit-style bottom keybinding bar: dim "  key  label"
// chips. It's the single place controls are advertised, so they're consistent
// and discoverable across every screen.
func KeyBar(hints []KeyHint) string {
	cells := make([]string, 0, len(hints))
	for _, h := range hints {
		if h.Key == "" {
			continue
		}
		cells = append(cells, styleKeyBarKey.Render(" "+h.Key+" ")+styleSettingsFoot.Render(" "+h.Label))
	}
	return strings.Join(cells, styleSettingsFoot.Render("   "))
}

// AppScreen renders a full-terminal screen: a bordered frame that fills the
// terminal (the same rectangle on every screen, so navigating between
// screens/tabs never resizes anything), with an optional title header, the body
// content, and a keybar pinned to the bottom. Designed to fit PuTTY's 80×24 and
// grow gracefully on larger terminals.
func AppScreen(w, h int, title, body, keybar string) string {
	if w < 10 || h < 6 {
		return body
	}
	inner := w - 4 // border(1)+pad(1) each side

	var head string
	if title != "" {
		head = styleSettingsTitle.Render(title)
	}
	content := head
	if body != "" {
		if content != "" {
			content += "\n"
		}
		content += body
	}

	// Pad each content line to the inner width so the border is rectangular.
	padded := padLines(content, inner)
	contentLines := strings.Count(padded, "\n") + 1

	// Pin the keybar to the bottom of the frame by padding blank rows between
	// the content and the keybar.
	avail := h - 2                  // inside the border (no vertical padding)
	pad := avail - contentLines - 1 // -1 for the keybar row itself
	if pad < 0 {
		pad = 0
	}
	out := padded + strings.Repeat("\n", pad)
	if keybar != "" {
		out += padLines(keybar, inner)
	} else {
		out += strings.Repeat(" ", inner)
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colPrimary).
		Padding(0, 1).
		Width(inner)
	return box.Render(out)
}

// AppScreenScroll is AppScreen for scrollable bodies: the caller passes a body
// that is ALREADY exactly bodyH lines tall (typically a viewport.View()), so this
// does no bottom-padding — it just frames title + body + keybar. bodyH is the
// number of rows reserved for the body (the viewport height).
func AppScreenScroll(w, h int, title, body string, bodyH int, keybar string) string {
	if w < 10 || h < 6 {
		return body
	}
	inner := w - 4
	avail := h - 2 // inside the border

	rows := 0
	head := ""
	if title != "" {
		head = padLines(title, inner)
		rows = 1
	}
	bh := bodyH
	if max := avail - rows - 1; bh > max {
		bh = max
	}
	if bh < 1 {
		bh = 1
	}
	block := padBlock(body, inner, bh)

	out := head
	if head != "" {
		out += "\n"
	}
	out += block + "\n" + padLines(keybar, inner)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colPrimary).
		Padding(0, 1).
		Width(inner)
	return box.Render(out)
}

// padLines right-pads each line of s to width cells (truncating none), so a
// block renders as a clean rectangle inside a fixed-width border.
func padLines(s string, width int) string {
	var b strings.Builder
	for i, ln := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		w := lipgloss.Width(ln)
		if w < width {
			b.WriteString(ln)
			b.WriteString(strings.Repeat(" ", width-w))
		} else {
			b.WriteString(ln)
		}
	}
	return b.String()
}

// padBlock pads a block to exactly width cells per line AND exactly n lines
// (truncating neither, padding both), so two blocks can be joined side by side
// at equal height.
func padBlock(s string, width, nLines int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < nLines {
		lines = append(lines, "")
	}
	for i, ln := range lines {
		w := lipgloss.Width(ln)
		if w < width {
			lines[i] = ln + strings.Repeat(" ", width-w)
		}
	}
	return strings.Join(lines[:nLines], "\n")
}

// TwoPane renders a full-screen frame split into a narrow left nav pane and a
// wide right detail pane, with a header line on top and the keybar on the
// bottom (both spanning). Both panes fill the height — no wasted space. This is
// the lazygit/k9s-style layout used by the settings screen.
func TwoPane(w, h int, header, left, right, keybar string) string {
	if w < 30 || h < 8 {
		return AppScreen(w, h, "", right, keybar)
	}
	inner := w - 4 // border(1)+pad(1) each side
	leftW := 24
	if leftW > inner/3 {
		leftW = inner / 3
	}
	if leftW < 14 {
		leftW = 14
	}
	rightW := inner - leftW - 1 // -1 for the divider column
	bodyH := h - 2 - 2          // inside border, minus header row + keybar row
	if bodyH < 3 {
		bodyH = 3
	}

	leftLines := strings.Split(padBlock(left, leftW, bodyH), "\n")
	rightLines := strings.Split(padBlock(right, rightW, bodyH), "\n")
	divider := styleSettingsFoot.Render("│")
	var mid strings.Builder
	for i := 0; i < bodyH; i++ {
		if i > 0 {
			mid.WriteByte('\n')
		}
		mid.WriteString(leftLines[i])
		mid.WriteString(divider)
		mid.WriteString(rightLines[i])
	}
	out := padLines(header, inner) + "\n" + mid.String() + "\n" + padLines(keybar, inner)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colPrimary).
		Padding(0, 1).
		Width(inner)
	return box.Render(out)
}
