package tui

import (
	"fmt"
	"strings"
	"time"
)

// Fixed-width numerals for anything that ticks on screen (tokens, elapsed,
// percentages), so neighbouring text never shifts as values change.

// tokensWidth is the widest formatTokens result ("999k", "9.9M").
const tokensWidth = 5

// FormatTokens abbreviates a token count: 42 → "42", 1200 → "1.2k",
// 45000 → "45k", 2300000 → "2.3M".
func FormatTokens(n int) string { return formatTokens(n) }

// formatTokens abbreviates a token count: 1200 → "1.2k", 42 → "42".
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}

// fixedTokens is formatTokens right-aligned to tokensWidth, with a leading
// "~" when the value is an estimate rather than a provider-reported count.
func fixedTokens(n int, estimated bool) string {
	s := formatTokens(n)
	if estimated {
		s = "~" + s
	}
	return fmt.Sprintf("%*s", tokensWidth+1, s)
}

// FixedTokens is the exported fixedTokens for the root's status line.
func FixedTokens(n int, estimated bool) string { return fixedTokens(n, estimated) }

// formatElapsed renders a duration right-aligned to 7 cells:
// "    12s" / "59m 04s" / " 1h 02m".
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	var out string
	switch {
	case s < 60:
		out = fmt.Sprintf("%ds", s)
	case s < 3600:
		out = fmt.Sprintf("%dm %02ds", s/60, s%60)
	default:
		out = fmt.Sprintf("%dh %02dm", s/3600, (s%3600)/60)
	}
	return fmt.Sprintf("%7s", out)
}

// ContextMeter renders "Context ██████░░░░  42%": the bar fills as the
// context window is used and is always paired with the exact percentage.
// Colour follows the 70/90 convention (normal / amber / red). With no known
// window it degrades to the token count alone.
func ContextMeter(used, window int, estimated bool, cells int) string {
	label := styleSystem.Render("Context ")
	if window <= 0 {
		return label + styleSystem.Render(strings.TrimSpace(fixedTokens(used, estimated)))
	}
	p := fracPct(frac01(float64(used), float64(window)))
	col, bold := barOK, false
	switch {
	case p >= 90:
		col, bold = barCrit, true
	case p >= 70:
		col = barWarn
	}
	pct := fmt.Sprintf("%3d%%", p)
	if estimated {
		pct = "~" + pct
	}
	return label + barFill(p, cells, col, bold) + " " + styleSystem.Render(pct)
}
