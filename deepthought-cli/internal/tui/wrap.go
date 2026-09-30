package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// codeGutter prefixes the continuation segments of a hard-wrapped code line so
// a reader can tell a wrapped line from a new one.
const codeGutter = "↪ "

// wrapTranscript wraps raw (unstyled) text to w cells and returns the rows.
// Prose word-wraps and hard-breaks tokens longer than w (URLs, paths). Lines
// inside ``` fences keep their indentation and hard-wrap with a continuation
// gutter; fence lines themselves are kept intact unless wider than w.
func wrapTranscript(text string, w int) []string {
	if w < 4 {
		w = 4
	}
	var out []string
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			out = append(out, hardCut(line, w)...)
			continue
		}
		if inFence {
			out = append(out, hardCut(strings.ReplaceAll(line, "\t", "    "), w)...)
			continue
		}
		if ansi.StringWidth(line) <= w {
			out = append(out, line)
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(line, w, ""), "\n")...)
	}
	return out
}

// hardCut splits one line into w-cell segments; segments after the first carry
// codeGutter and are correspondingly narrower.
func hardCut(line string, w int) []string {
	if ansi.StringWidth(line) <= w {
		return []string{line}
	}
	out := []string{ansi.Truncate(line, w, "")}
	rest := ansi.TruncateLeft(line, w, "")
	cw := w - ansi.StringWidth(codeGutter)
	for rest != "" && ansi.StringWidth(rest) > 0 {
		seg := ansi.Truncate(rest, cw, "")
		if seg == "" { // a grapheme wider than cw; emit it rather than loop
			seg, rest = rest, ""
		} else {
			rest = ansi.TruncateLeft(rest, cw, "")
		}
		out = append(out, codeGutter+seg)
	}
	return out
}

// wrapPrefixed wraps raw text to w cells with a hanging indent: the first row
// starts with first, continuation rows with cont (same cell width), and every
// row is styled separately so no escape state leaks across rows.
func wrapPrefixed(text string, w int, first, cont string, st lipgloss.Style) string {
	rows := wrapTranscript(text, w-ansi.StringWidth(first))
	for i, r := range rows {
		p := cont
		if i == 0 {
			p = first
		}
		rows[i] = st.Render(p + r)
	}
	return strings.Join(rows, "\n")
}

var leadingSGR = regexp.MustCompile(`^(\x1b\[[0-9;:]*m)+`)

// wrapStyled wraps an already-styled string to w cells. Each source line's
// leading SGR run is re-applied to its continuation rows and every row ends
// with a reset, so colour neither drops on wrapped rows nor bleeds into
// whatever is joined beside the transcript (the sidebar).
func wrapStyled(s string, w int) string {
	if w < 4 {
		w = 4
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if ansi.StringWidth(line) <= w {
			out = append(out, line)
			continue
		}
		lead := leadingSGR.FindString(line)
		for i, r := range strings.Split(ansi.Wrap(line, w, ""), "\n") {
			if i > 0 {
				r = lead + r
			}
			out = append(out, r+"\x1b[m")
		}
	}
	return strings.Join(out, "\n")
}
