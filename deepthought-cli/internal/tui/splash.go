package tui

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	splashTagline = "Research computing harness · University of Alberta / AMII"
	splashHint    = "↑/↓ choose · enter select"
)

// splashSpinner aliases the default braille set (see spinners.go).
var splashSpinner = DefaultSpinner

// BootInfo is what the home screen's info block shows. Set once at
// construction from the live settings and host; Server is refreshed by the
// root after a login.
type BootInfo struct {
	Model    string // active chat-role model label
	Provider string // provider name serving it
	Ready    bool   // false when no API key / nothing configured
	Version  string // "DeepThought <build>"
	Server   string // "Standalone — local settings only" / "Connected as …"
	Host     string
	Cwd      string
}

// SplashModel is the home screen: wordmark, tagline, version, an info block
// (model, server, host, working directory) and the start actions. It is a
// plain struct, not a tea.Model — the root wraps it.
type SplashModel struct {
	spin      spinner.Model
	spinFrame int // our own frame counter; the spinner's frame field is unexported
	verb      string
	boot      BootInfo
	width     int
	height    int
	selected  int
	notice    string
}

// NewSplashModel builds the home screen. sessionID is kept for API
// stability; nothing on the screen varies per session any more.
func NewSplashModel(boot BootInfo, sessionID string) SplashModel {
	_ = sessionID
	sp := spinner.New(spinner.WithSpinner(splashSpinner), spinner.WithStyle(styleVerb))
	return SplashModel{spin: sp, verb: "Booting", boot: boot}
}

// WithServer updates the server line (after login or disconnect).
func (m SplashModel) WithServer(line string) SplashModel {
	m.boot.Server = line
	return m
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
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		m.spinFrame++
		return m, cmd
	case tea.KeyPressMsg:
		switch msg.String() {
		case "down", "tab":
			m.selected = (m.selected + 1) % 5
			m.notice = ""
		case "up", "shift+tab":
			m.selected = (m.selected + 4) % 5
			m.notice = ""
		case "enter":
			if m.selected == 0 {
				return m, func() tea.Msg { return SplashAdvanceMsg{} }
			}
			if m.selected >= 2 {
				kind := []string{"discover", "menu", "install"}[m.selected-2]
				return m, func() tea.Msg { return WorkspaceAction{Kind: kind} }
			}
			return m, func() tea.Msg { return SplashServerLoginMsg{} }
		}
	}
	return m, nil
}

// Resize stores geometry for View. Splash owns no size-sensitive components.
func (m SplashModel) Resize(w, h int) SplashModel {
	m.width, m.height = w, h
	return m
}

// WithNotice sets the inline notice line (used by the app to surface login
// results while staying on the splash).
func (m SplashModel) WithNotice(text string) SplashModel {
	m.notice = text
	return m
}

// View composes the home screen as one centered block. Below 90 columns or
// 24 rows it goes compact: plain-text name, no tagline, model line only.
func (m SplashModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	compact := m.width < 90 || m.height < 24
	choices := []string{"  Run standalone", "  Log in to server", "  Set up AI providers", "  Explore / Settings", "  Install on another machine"}
	choices[m.selected] = "› " + strings.TrimSpace(choices[m.selected])

	var rows []string
	if compact {
		rows = append(rows, styleName.Render("DeepThought")+"  "+styleVersion.Render(m.version()))
	} else {
		rows = append(rows, wordmarkHeader(m.width, m.height-18), "",
			styleVersion.Render(splashTagline), styleVersion.Render(m.version()))
	}
	rows = append(rows, "", m.infoBlock(compact), "")
	rows = append(rows, styleName.Render(choices[0]))
	for _, c := range choices[1:] {
		rows = append(rows, styleVersion.Render(c))
	}
	rows = append(rows, styleVersion.Render(m.notice), styleVersion.Render(splashHint))
	block := lipgloss.JoinVertical(lipgloss.Center, rows...)
	return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(placeCenter(m.width, m.height, block))
}

func (m SplashModel) version() string {
	return orDefault(m.boot.Version, "DeepThought (development build)")
}

// infoBlock is the left-aligned label/value block under the tagline.
func (m SplashModel) infoBlock(compact bool) string {
	row := func(label, value string) string {
		return styleSettingsKey.Render(fmt.Sprintf("%-7s", label)) + " " + styleVersion.Render(clipLine(value, max(10, m.width-12)))
	}
	rows := []string{row("model", m.statusLine())}
	if !compact {
		rows = append(rows,
			row("server", orDefault(m.boot.Server, "Standalone — local settings only")),
			row("host", orDefault(m.boot.Host, "unknown")),
			row("cwd", orDefault(m.boot.Cwd, "unknown")),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// statusLine renders the boot model + provider (or the setup hint).
func (m SplashModel) statusLine() string {
	if !m.boot.Ready {
		return "No model configured → Set up AI providers"
	}
	if m.boot.Model == "" {
		return "ready"
	}
	return m.boot.Model + " · " + m.boot.Provider
}

// wordmarkHeader picks the biggest "DeepThought" wordmark that fits
// width×height (standard font, then small), painted along the brand
// gradient; plain styled text when no art fits.
func wordmarkHeader(width, height int) string {
	for _, cand := range [][]string{
		bigTextIn("standard", "DeepThought"),
		bigTextIn(smallTextFont, "DeepThought"),
	} {
		if len(cand) > 0 && widestRow(cand) <= width && len(cand) <= height {
			return paintGradient(cand)
		}
	}
	return styleName.Render("DeepThought")
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
