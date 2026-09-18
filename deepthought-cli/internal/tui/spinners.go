package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
)

// ActivityHeight is the fixed 1-row strip above the chat input that shows the
// thinking/loading indicator (or a queued-input / bash-mode hint when idle).
const ActivityHeight = 1

// Spinner frame sets. Braille is the default; the others are selectable later.
var (
	spinnerBraille = spinner.Spinner{
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		FPS:    120 * time.Millisecond,
	}
	spinnerBridge = spinner.Spinner{
		Frames: []string{"·|·", "·/·", "·—·", "·\\·"},
		FPS:    120 * time.Millisecond,
	}
	spinnerDots = spinner.Spinner{
		Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
		FPS:    100 * time.Millisecond,
	}
	spinnerMoon = spinner.Spinner{
		Frames: []string{"◐", "◓", "◑", "◒"},
		FPS:    150 * time.Millisecond,
	}
	spinnerBar = spinner.Spinner{
		Frames: []string{" ▏", " ▎", " ▍", " ▌", " ▋", " ▊", " ▉", " █", " ▉", " ▊", " ▋", " ▌", " ▍", " ▎"},
		FPS:    80 * time.Millisecond,
	}
)

// DefaultSpinner is the frame set used by splash + chat activity.
var DefaultSpinner = spinnerBraille

// Activity verbs (trimmed from the reference spinnerVerbs list, with a few
// HHGTTG-flavored ones). Seeded per-turn so the activity line feels alive.
var activityVerbs = []string{
	"Accomplishing", "Architecting", "Beaming", "Bootstrapping", "Brewing",
	"Calculating", "Cascading", "Cerebrating", "Channeling", "Coalescing",
	"Cogitating", "Composing", "Computing", "Concocting", "Contemplating",
	"Crafting", "Crunching", "Deciphering", "Deliberating", "Envisioning",
	"Forging", "Generating", "Harmonizing", "Hyperspacing", "Ideating",
	"Imagining", "Inferring", "Manifesting", "Marinating", "Mulling",
	"Musing", "Noodling", "Orbiting", "Orchestrating", "Percolating",
	"Pondering", "Pontificating", "Processing", "Quantumizing", "Reticulating",
	"Ruminating", "Synthesizing", "Thinking", "Tinkering", "Transmuting",
	"Warping", "Wrangling",
	// HHGTTG-flavored
	"Improbabilitizing", "Toweling", "Don't-Panic-ing", "42-ing",
}

// verbFor picks a verb deterministically from seed (e.g. session+turn id) so
// each turn gets a different word without touching rand/time in the model path.
func verbFor(seed string) string {
	var n int
	for _, r := range seed {
		n += int(r)
	}
	if n < 0 {
		n = -n
	}
	return activityVerbs[n%len(activityVerbs)]
}

// formatTokens abbreviates a token count: 1200 → "1.2k", 42 → "42".
func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%dk", n/1000)
}

// formatElapsed renders a duration as "12s" / "1m 4s" / "1h 2m".
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh %dm", s/3600, (s%3600)/60)
	}
}

// ActivityState is what the activity line renders.
type ActivityState struct {
	Busy       bool
	Streaming  bool
	Verb       string
	Tokens     int // live / last-turn token count (0 = omit)
	Elapsed    time.Duration
	Queued     string // pending queued input chip
	Awaiting   bool   // Queen approval on screen
	SpinFrame  int
	BashHint   bool // idle: advertise "! for bash"
}

// RenderActivity paints the 1-row strip above the input. Always returns exactly
// one visual row (padded/truncated to w) so layout stays stable.
func RenderActivity(w int, st ActivityState) string {
	var parts []string
	switch {
	case st.Awaiting:
		parts = append(parts, styleToolAsk.Render("⚠ allow? [y] once  [a] task  [A] always  [n] deny  [d] never"))
	case st.Busy:
		glyph := spinnerGlyph(st.SpinFrame)
		verb := st.Verb
		if verb == "" {
			verb = "Thinking"
		}
		parts = append(parts, glyph+" "+styleSystem.Render(verb+"…"))
		if st.Tokens > 0 {
			parts = append(parts, styleSystem.Render(formatTokens(st.Tokens)+" tokens"))
		}
		if st.Elapsed > 0 {
			parts = append(parts, styleSystem.Render(formatElapsed(st.Elapsed)))
		}
		parts = append(parts, styleSystem.Render("esc to interrupt"))
	case st.Queued != "":
		parts = append(parts, styleSystem.Render("queued: "+truncate(st.Queued, max(8, w/3))))
	default:
		// Idle: short discoverability hints.
		hint := "? for shortcuts"
		if st.BashHint {
			hint = "! for bash · ? for shortcuts"
		}
		parts = append(parts, styleSystem.Render(hint))
	}
	line := strings.Join(parts, styleStatusSep.Render(" · "))
	if ww := lipgloss.Width(line); ww > w && w > 0 {
		line = lipgloss.NewStyle().MaxWidth(w).Render(line)
	} else if w > 0 {
		pad := w - lipgloss.Width(line)
		if pad > 0 {
			line += strings.Repeat(" ", pad)
		}
	}
	return line
}

