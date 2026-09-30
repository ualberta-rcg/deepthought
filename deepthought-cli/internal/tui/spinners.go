package tui

import (
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

// Activity verbs name the turn's actual phase — no rotating flavor words.
const (
	verbThinking = "Thinking"
	verbWriting  = "Writing"
	verbTool     = "Running tool"
)

// ActivityState is what the activity line renders.
type ActivityState struct {
	Busy      bool
	Streaming bool
	Verb      string
	Tokens    int  // live token count while busy (0 = omit)
	Estimated bool // Tokens / Context are chars/4 estimates, not provider counts
	Context   int  // idle: prompt tokens of the last turn (window fill)
	Window    int  // idle: the active model's context window (0 = unknown)
	Elapsed   time.Duration
	Queued    string // pending queued input chip
	Awaiting  bool   // Queen approval on screen
	SpinFrame int
	BashHint  bool // idle: advertise "! for bash"
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
		// Both segments are always present and fixed-width so nothing to
		// their right shifts while the numbers tick.
		parts = append(parts, styleSystem.Render(fixedTokens(st.Tokens, st.Estimated)+" tokens"))
		parts = append(parts, styleSystem.Render(formatElapsed(st.Elapsed)))
		parts = append(parts, styleSystem.Render("esc to interrupt"))
	case st.Queued != "":
		parts = append(parts, styleSystem.Render("queued: "+truncate(st.Queued, max(8, w/3))))
	default:
		// Idle: context fill of the last turn, then discoverability hints.
		if st.Context > 0 {
			parts = append(parts, ContextMeter(st.Context, st.Window, st.Estimated, 10))
		}
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
