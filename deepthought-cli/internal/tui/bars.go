package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// The Cluster screen (F10) borrows the visual language of the hand-written
// /usr/local/bin/vulcan-status MOTD: ▓░ bars where FILL LENGTH is the primary
// signal and color is reinforcement only, on a colorblind-safe palette (teal /
// orange / bold light-red, which differ along the blue↔yellow axis AND in
// lightness — never green-vs-red alone). These helpers render that language
// inside the TUI. (The fixed single-color bar() in styles.go is the simpler
// Status-page variant; these are the threshold-colored ones.)

// Cluster-utilization ramp colors (fill), best → worst.
var (
	barOK   = lipgloss.Color("79")  // teal — comfortable
	barWarn = lipgloss.Color("214") // orange — getting full
	barCrit = lipgloss.Color("203") // light red — nearly full (rendered bold)
)

// Storage ramp (its own scale, not the utilization one): teal < 33, blue < 66,
// bold red ≥ 66.
var (
	diskOK   = lipgloss.Color("79") // teal
	diskWarn = lipgloss.Color("75") // blue
)

// Fairshare tier colors, best → worst (lightness/bold first, hue second).
var (
	fsBoosted   = lipgloss.Color("48")  // bold turquoise — top priority
	fsAhead     = lipgloss.Color("79")  // teal
	fsNominal   = lipgloss.Color("250") // near-white — neutral middle
	fsBehind    = lipgloss.Color("214") // orange
	fsThrottled = lipgloss.Color("203") // bold light red
)

// barFill paints width cells, pct% filled, in the given color (bold optional).
// Returns the ANSI string for the bar only.
func barFill(pct, width int, col color.Color, bold bool) string {
	if width < 1 {
		width = 1
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * width / 100
	st := lipgloss.NewStyle().Foreground(col).Bold(bold)
	return st.Render(strings.Repeat("▓", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(strings.Repeat("░", width-filled))
}

// healthBar is a cluster-utilization bar: teal under 80, orange 80–95, bold red
// at/above 95. frac is 0–1.
func healthBar(frac float64, width int) string {
	p := fracPct(frac)
	col, bold := barOK, false
	if p >= 95 {
		col, bold = barCrit, true
	} else if p >= 80 {
		col = barWarn
	}
	return barFill(p, width, col, bold)
}

// diskBar is a storage bar: teal under 33, blue 33–66, bold red at/above 66.
func diskBar(frac float64, width int) string {
	p := fracPct(frac)
	col, bold := diskOK, false
	if p >= 66 {
		col, bold = barCrit, true
	} else if p >= 33 {
		col = diskWarn
	}
	return barFill(p, width, col, bold)
}

// tierColor maps a fairshare tier label to its color and whether to bold it.
func tierColor(label string) (color.Color, bool) {
	switch label {
	case "boosted":
		return fsBoosted, true
	case "ahead":
		return fsAhead, false
	case "behind":
		return fsBehind, false
	case "throttled":
		return fsThrottled, true
	default: // nominal
		return fsNominal, false
	}
}

// sectionHead renders a "» Title" block header, bold, with an optional dim
// extra on the right (e.g. "0 running · 1 pending"). Mirrors vulcan-status hdr.
func sectionHead(title string, extra ...string) string {
	s := lipgloss.NewStyle().Bold(true).Foreground(colPrimary).Render("» " + title)
	if len(extra) > 0 && extra[0] != "" {
		s += "  " + dimNote(extra[0])
	}
	return s
}

// dimNote is a muted explanatory line (the "what this means" text under a block).
func dimNote(s string) string {
	return lipgloss.NewStyle().Foreground(colDim).Render(s)
}

// hintNote is the dim "for more detail, run X" footer of a block.
func hintNote(cmd string) string {
	return dimNote("  → " + cmd)
}

// fracPct clamps a 0–1 fraction to a 0–100 integer percent.
func fracPct(frac float64) int {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return int(frac*100 + 0.5)
}
