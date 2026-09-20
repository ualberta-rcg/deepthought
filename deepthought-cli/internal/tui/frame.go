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
		Width(w) // TOTAL width: content capacity = w-4 = inner (lipgloss v2 Width includes border+pad)
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
		Width(w) // TOTAL width: content capacity = w-4 = inner (lipgloss v2 Width includes border+pad)
	return box.Render(out)
}

// padLines right-pads each line of s to width cells, CLIPPING any line wider
// than width (the safety net that keeps a bordered frame rectangular even when
// a row overflows — better a clipped cell than a wrapped frame), so a block
// renders as a clean rectangle inside a fixed-width border.
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
			b.WriteString(clipLine(ln, width))
		}
	}
	return b.String()
}

// padBlock pads a block to exactly width cells per line AND exactly n lines
// (clipping overwide lines — see padLines), so two blocks can be joined side
// by side at equal height.
func padBlock(s string, width, nLines int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < nLines {
		lines = append(lines, "")
	}
	for i, ln := range lines {
		w := lipgloss.Width(ln)
		if w < width {
			lines[i] = ln + strings.Repeat(" ", width-w)
		} else {
			lines[i] = clipLine(ln, width)
		}
	}
	return strings.Join(lines[:nLines], "\n")
}

// clipLine truncates s to at most width visible cells, ANSI-aware (escape
// sequences carry no width and survive).
func clipLine(s string, width int) string {
	if width < 1 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

// screenTitle is the one title convention for every framed screen:
// "DeepThought › Name".
func screenTitle(name string) string {
	return "DeepThought › " + name
}

// emptyRow is the one empty-state vocabulary for framed screens: a dim
// "(no <noun> yet)" line. Pickers keep their "(nothing to choose)".
func emptyRow(noun string) string {
	return styleSettingsFoot.Render("(no " + noun + " yet)")
}

// truncatePad pads s to exactly n cells, truncating with an ellipsis when
// longer. (Data-column helper shared by Status/Cluster/Continue/Model chooser.)
func truncatePad(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s + strings.Repeat(" ", n-len(r))
}

// overlayCenter composites child over parent's canvas, both centered, by
// splicing each child row into the parent lines at the target column — a real
// in-screen modal (replaces the old picker that painted over a blank canvas).
// Parent escape sequences before the child survive; cells under the child are
// dropped (the child carries its own styling).
// OverlayCenter composites child centered over parent (exported: the root
// composites pickers over the active screen).
func OverlayCenter(parent, child string) string {
	pl := strings.Split(parent, "\n")
	cl := strings.Split(child, "\n")
	pw := 0
	for _, ln := range pl {
		if w := lipgloss.Width(ln); w > pw {
			pw = w
		}
	}
	cw := 0
	for _, ln := range cl {
		if w := lipgloss.Width(ln); w > cw {
			cw = w
		}
	}
	top := max(0, (len(pl)-len(cl))/2)
	left := max(0, (pw-cw)/2)

	out := make([]string, len(pl))
	for i := range pl {
		if i >= top && i < top+len(cl) {
			out[i] = spliceAt(pl[i], left, padLines(cl[i-top], cw))
		} else {
			out[i] = pl[i]
		}
	}
	return strings.Join(out, "\n")
}

// spliceAt returns line with cover inserted at visible column col: the line's
// cells before col, then cover, then the line's remainder after col+cover.
func spliceAt(line string, col int, cover string) string {
	cw := lipgloss.Width(cover)
	var pre strings.Builder
	vis := 0
	rs := []rune(line)
	for i := 0; i < len(rs); {
		r := rs[i]
		if r == 0x1b {
			// Consume the escape sequence (ESC [ params… final, or ESC x).
			j := i + 1
			if j < len(rs) && rs[j] == '[' {
				j++
				for j < len(rs) && !(rs[j] >= 0x40 && rs[j] <= 0x7E) {
					j++
				}
				if j < len(rs) {
					j++
				}
			} else if j < len(rs) {
				j++
			}
			if vis < col {
				pre.WriteString(string(rs[i:j]))
			}
			i = j
			continue
		}
		w := lipgloss.Width(string(r))
		if vis >= col+cw {
			// Past the cover: keep the raw remainder (escapes included).
			return pre.String() + cover + string(rs[i:])
		}
		if vis < col {
			pre.WriteRune(r)
		}
		// Cells within the cover zone are dropped (covered).
		vis += w
		i++
	}
	// Parent line ended inside/before the cover.
	left := pre.String()
	if lw := lipgloss.Width(left); lw < col {
		left += strings.Repeat(" ", col-lw)
	}
	return left + cover
}
