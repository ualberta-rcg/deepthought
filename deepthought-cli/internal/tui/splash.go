package tui

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	splashVersion = "DeepThought v0.0.1"
	splashHint    = "press any key to continue"
)

// splashSubtitles are the rotating HHGTTG one-liners shown under the DON'T
// PANIC wordmark. One is chosen per boot (see subtitleFor) so the splash feels
// alive without flickering.
var splashSubtitles = []string{
	"Share and Enjoy.",
	"Mostly Harmless.",
	"We Apologize for the Inconvenience.",
	"So Long, and Thanks for All the Fish.",
	"Bring a towel.",
	"42.",
}

// footerRows is the splash chrome below the wordmark: blank + subtitle +
// status + version + spinner + blank + hint.
const footerRows = 7

// splashSpinner aliases the default braille set (see spinners.go).
var splashSpinner = DefaultSpinner

// BootInfo is the resolved boot-time model/provider the splash status line
// shows. Set once at construction from the live settings.
type BootInfo struct {
	Model    string // active chat-role model label
	Provider string // provider name serving it
	Ready    bool   // false when no API key / nothing configured
}

// SplashModel is the boot screen: the big DON'T PANIC wordmark + status +
// rotating subtitle + version + spinner. It is a plain struct, not a
// tea.Model — the root wraps it.
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

// View composes the splash: the DON'T PANIC wordmark (dontPanicHeader picks
// the biggest rendering that fits), a rotating HHGTTG subtitle, a status line
// (model · provider), version + spinner, and the hint. The whole block is
// centered as one unit. Colors are the solid brand pair.
func (m SplashModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	header := dontPanicHeader(m.width, m.height-footerRows)

	block := lipgloss.JoinVertical(lipgloss.Center,
		header,
		"",
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

// dontPanicHeader picks the biggest DON'T PANIC wordmark that fits the given
// width×height, in order: the colossal font on one line, colossal stacked
// (DON'T over PANIC), the small font on one line, then styled plain text.
// Every art candidate is painted with paintGradient — a restrained left→right
// cyan→violet brand shade, static.
func dontPanicHeader(width, height int) string {
	for _, cand := range [][]string{
		bigTextIn(bigTextFont, "DON'T PANIC"),
		stackArt(bigTextIn(bigTextFont, "DON'T"), bigTextIn(bigTextFont, "PANIC")),
		bigTextIn(smallTextFont, "DON'T PANIC"),
	} {
		if len(cand) > 0 && widestRow(cand) <= width && len(cand) <= height {
			return paintGradient(cand)
		}
	}
	return styleName.Render("DON'T PANIC")
}

// stackArt joins two art blocks with one blank row between them.
func stackArt(top, bottom []string) []string {
	if len(top) == 0 || len(bottom) == 0 {
		return nil
	}
	out := make([]string, 0, len(top)+1+len(bottom))
	out = append(out, top...)
	out = append(out, "")
	return append(out, bottom...)
}

// paintGradient colors each rune of ASCII-art lines along the brand gradient
// keyed by column, so wide wordmarks shade gently from cyan (left) to violet
// (right).
func paintGradient(lines []string) string {
	w := widestRow(lines)
	var b strings.Builder
	for _, ln := range lines {
		for i, r := range ln {
			t := 0.0
			if w > 1 {
				t = float64(i) / float64(w-1)
			}
			b.WriteString(lipgloss.NewStyle().Foreground(brandGradient(t)).Render(string(r)))
		}
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// The gradient endpoints mirror the palette in styles.go (colPrimary cyan,
// colSecondary violet); keep them in sync on a retheme.
const (
	gradFrom = "#7DD3FC"
	gradTo   = "#C084FC"
)

// brandGradient lerps between the two brand colors — cyan at t=0, violet at
// t=1.
func brandGradient(t float64) color.Color {
	return lipgloss.Color(brandGradientHex(t))
}

// brandGradientHex is brandGradient's #RRGGBB string form (and the test seam).
func brandGradientHex(t float64) string {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	r0, g0, b0 := hexRGB(gradFrom)
	r1, g1, b1 := hexRGB(gradTo)
	mix := func(a, c int) int { return a + int(t*float64(c-a)) }
	return "#" + hexByte(mix(r0, r1)) + hexByte(mix(g0, g1)) + hexByte(mix(b0, b1))
}

// hexRGB parses a #rrggbb color string into its components.
func hexRGB(s string) (r, g, b int) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return int(v >> 16 & 0xFF), int(v >> 8 & 0xFF), int(v & 0xFF)
}

func hexByte(v int) string {
	const hex = "0123456789ABCDEF"
	return string(hex[v>>4&0xF]) + string(hex[v&0xF])
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
