package tui

import (
	"strings"

	"github.com/common-nighthawk/go-figure"
)

// bigTextFont is the figlet font used for the splash wordmark. "colossal"
// renders "DeepThought" at an effective 11 rows once blank edge rows are
// trimmed (taller than the old 7-letter name — the layout tolerates it).
// Pulled in via go-figure, which embeds its fonts (bindata) so the binary
// stays self-contained. TODO: vendor just this one .flf to drop the unused
// 148 fonts if binary size ever matters.
const bigTextFont = "colossal"

// bigText renders s as ASCII-art lines, trimmed for layout: trailing spaces
// stripped per row and fully-blank edge rows dropped. Returns nil if the font
// can't render the text (the caller falls back to plain text). Color is the
// splash header's job — it paints whole composed rows so the wordmark shares
// the logo's band and drift.
func bigText(s string) []string {
	if s == "" {
		return nil
	}
	out := figure.NewFigure(s, bigTextFont, true).String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}

// doubleRows repeats every line, turning colossal's 8 rows into the 16-row
// wordmark the splash pairs with the 20-row logo. The blocky font doubles
// cleanly, and the pair keeps the old 2-row inset at top and bottom.
func doubleRows(lines []string) []string {
	out := make([]string, 0, len(lines)*2)
	for _, ln := range lines {
		out = append(out, ln, ln)
	}
	return out
}
