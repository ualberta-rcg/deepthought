package kit

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Fit makes s exactly w cells wide: truncated with the ellipsis glyph when
// longer, space-padded when shorter. ANSI-aware.
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := lipgloss.Width(s)
	if sw > w {
		s = ansi.Truncate(s, w, G().Ellipsis)
		sw = lipgloss.Width(s)
	}
	if sw < w {
		s += strings.Repeat(" ", w-sw)
	}
	return s
}

// Block makes every line of lines exactly w wide and the block exactly h
// lines tall (h <= 0 keeps the line count).
func Block(lines []string, w, h int) []string {
	if h > 0 {
		if len(lines) > h {
			lines = lines[:h]
		}
		for len(lines) < h {
			lines = append(lines, "")
		}
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = Fit(ln, w)
	}
	return out
}

// Size is one card's height demand for FitHeights.
type Size struct {
	Min, Preferred int
}

// FitHeights shares h rows between cards listed in priority order (most
// important first). Every card that fits gets at least Min; leftover rows go
// to cards in priority order up to Preferred. Cards whose Min no longer fits
// get 0 (dropped), lowest priority first — a fixed, documented shrink order.
func FitHeights(sizes []Size, h int) []int {
	out := make([]int, len(sizes))
	used := 0
	for i, s := range sizes {
		if used+s.Min <= h {
			out[i] = s.Min
			used += s.Min
		}
	}
	for i, s := range sizes {
		if out[i] == 0 && s.Min > 0 {
			continue
		}
		grow := min(s.Preferred-out[i], h-used)
		if grow > 0 {
			out[i] += grow
			used += grow
		}
	}
	return out
}
