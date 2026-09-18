package tui

import (
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	splashName    = "DeepThought"      // rendered as tall ASCII art beside the rainbow mark
	splashTagline = "Don't Panic." // permanent anchor on the splash
	splashVersion = "DeepThought v0.0.1"
	splashHint    = "press any key to continue"
)

// splashSubtitles are the rotating HHGTTG one-liners shown as a secondary
// accent under the permanent "Don't Panic." tagline. One is chosen per boot
// (see subtitleFor) so the splash feels alive without flickering.
var splashSubtitles = []string{
	"Share and Enjoy.",
	"Mostly Harmless.",
	"We Apologize for the Inconvenience.",
	"So Long, and Thanks for All the Fish.",
	"Bring a towel.",
	"42.",
}

const (
	splashDriftFrames = 4 // spinner frames per one-row upward rainbow drift (~0.5 s/row)
	headerGapCols     = 1 // cells between the mark's right edge and the wordmark
	footerRows        = 7 // blank + subtitle + status + version + spinner + blank + hint
)

// splashSpinner aliases the default braille set (see spinners.go).
var splashSpinner = DefaultSpinner

// BootInfo is the resolved boot-time model/provider the splash status line
// shows. Set once at construction from the live settings.
type BootInfo struct {
	Model    string // active chat-role model label
	Provider string // provider name serving it
	Ready    bool   // false when no API key / nothing configured
}

// SplashModel is the boot screen: mark + wordmark + status + rotating
// subtitle + version + spinner. It is a plain struct, not a tea.Model — the
// root wraps it.
type SplashModel struct {
	spin      spinner.Model
	spinFrame int // our own frame counter; the spinner's frame field is unexported
	verb      string
	boot      BootInfo
	sub       string // chosen subtitle for this boot
	width     int
	height    int
}

// NewSplashModel builds a splash with the braille spinner. boot feeds the
// status line; sessionID seeds the per-boot subtitle (no time/rand — both are
// forbidden in the model path).
func NewSplashModel(boot BootInfo, sessionID string) SplashModel {
	sp := spinner.New(spinner.WithSpinner(splashSpinner), spinner.WithStyle(styleVerb))
	return SplashModel{spin: sp, verb: "Booting", boot: boot, sub: subtitleFor(sessionID)}
}

// subtitleFor picks one HHGTTG subtitle deterministically from sessionID, so
// each launch shows a different line without touching time or rand.
func subtitleFor(sessionID string) string {
	var n int
	for _, r := range sessionID {
		n += int(r)
	}
	return splashSubtitles[n%len(splashSubtitles)]
}

// Init kicks the spinner (its Update reschedules later ticks). The splash stays
// until the first keypress — there's no auto-advance timer (pass --no-splash to
// skip it entirely).
func (m SplashModel) Init() tea.Cmd {
	return func() tea.Msg { return spinner.TickMsg{} } // first spin frame, now
}

// Update returns the concrete SplashModel type. On any key it asks the root to
// advance — the root decides whether that means a new chat or (when no model is
// configured) the Settings add-provider area. HoldDoneMsg is accepted
// defensively (stale timers from an earlier screen); splash arms none itself.
func (m SplashModel) Update(msg tea.Msg) (SplashModel, tea.Cmd) {
	switch msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		m.spinFrame++
		return m, cmd
	case tea.KeyPressMsg, HoldDoneMsg:
		return m, func() tea.Msg { return SplashAdvanceMsg{} }
	}
	return m, nil
}

// Resize stores geometry for View. Splash owns no size-sensitive components.
func (m SplashModel) Resize(w, h int) SplashModel {
	m.width, m.height = w, h
	return m
}

// View composes the header — the rainbow mark and the colossal "DeepThought"
// wordmark (splashHeader picks the biggest layout that fits) — then the
// permanent "Don't Panic." tagline, a rotating HHGTTG subtitle accent, a
// status line (model · provider), version + spinner, and the hint. The whole
// block is centered as one unit; the rainbow drifts slowly upward.
func (m SplashModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	header := splashHeader(m.width, m.height-footerRows, m.spinFrame/splashDriftFrames, wordmark())

	block := lipgloss.JoinVertical(lipgloss.Center,
		header,
		"",
		styleTagline.Render(splashTagline),
		styleVersion.Render(m.sub),
		styleVersion.Render(m.statusLine()),
		styleVersion.Render(splashVersion),
		spinnerGlyph(m.spinFrame)+" "+styleVerb.Render(m.verb),
		"",
		styleVersion.Render(splashHint),
	)
	return placeCenter(m.width, m.height, block)
}

// statusLine renders the boot model + provider (or a not-ready hint).
func (m SplashModel) statusLine() string {
	if !m.boot.Ready {
		return "⚠ no API key set — configure a provider in Settings"
	}
	if m.boot.Model == "" {
		return "ready"
	}
	return m.boot.Model + " · " + m.boot.Provider
}

// wordmark renders the name as colossal ASCII art (bigText); splashHeader
// centers it against the mark at whatever height the font gives it.
func wordmark() []string {
	return bigText(splashName)
}

// splashHeader picks the biggest mark+wordmark layout that fits width×height
// (height is what remains above the footer):
//
//  1. side by side — the mark and the wordmark as two pictures, vertically
//     centered against each other; the pair is centered as one unit.
//  2. stacked — the full-size mark over the wordmark, each centered.
//  3. compact — the shrunken mark alone; the name rides the version line.
//
// drift is the rainbow's upward drift in rows.
func splashHeader(width, height, drift int, word []string) string {
	if len(word) == 0 {
		word = []string{splashName}
	}

	if need := markFull.width() + headerGapCols + widestRow(word); width >= need && height >= markFull.rows {
		// Two pictures: each paints itself; JoinHorizontal pads both to
		// rectangles, so Align(Center) pads uniformly and the slant survives.
		pair := lipgloss.JoinHorizontal(lipgloss.Center,
			markFull.render(drift),
			strings.Repeat(" ", headerGapCols),
			paintWord(word, drift))
		return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(pair)
	}

	if width >= widestRow(word) && height >= markFull.rows+1+len(word) {
		// Stacked: mark centered by its widest row (uniform pad keeps the
		// slant), wordmark centered beneath, the sweep continuing downward.
		out := make([]string, 0, markFull.rows+1+len(word))
		markPad := strings.Repeat(" ", max(0, (width-markFull.width())/2))
		for r := 0; r < markFull.rows; r++ {
			out = append(out, markPad+paintRow(markFull.row(r), r, drift))
		}
		out = append(out, "")
		wordPad := strings.Repeat(" ", max(0, (width-widestRow(word))/2))
		for i, wl := range word {
			out = append(out, wordPad+paintRow(wl, markFull.rows+i, drift))
		}
		return strings.Join(out, "\n")
	}

	// Compact: the small mark, centered (plain ASCII if even that is wide).
	if width < markCompact.width() {
		return markCompact.renderPlain()
	}
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(markCompact.render(drift))
}

// paintWord colors each wordmark row with the band of the mark row it will
// sit beside once the pair is vertically centered, so both pictures share a
// hue per row.
func paintWord(word []string, drift int) string {
	top := max(0, (markFull.rows-len(word))/2)
	styled := make([]string, len(word))
	for i, wl := range word {
		styled[i] = paintRow(wl, top+i, drift)
	}
	return strings.Join(styled, "\n")
}

// paintRow colors one header line with the spectrum band for that row at the
// current drift, measured against the full-size mark.
func paintRow(line string, r, drift int) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(bandForRow(r, drift, markFull.rows))).Render(line)
}

// widestRow returns the visible width of the widest line.
func widestRow(lines []string) int {
	w := 0
	for _, ln := range lines {
		if lw := lipgloss.Width(ln); lw > w {
			w = lw
		}
	}
	return w
}
