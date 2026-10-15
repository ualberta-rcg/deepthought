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
	splashTagline = "★ DEEPTHOUGHT · RESEARCH COMPUTING HARNESS · UNIVERSITY OF ALBERTA / AMII ★"
	splashHint    = "↑/↓ or 1-5 choose · enter start"
)

// splashSpinner aliases the default braille set (see spinners.go).
var splashSpinner = DefaultSpinner

// BootInfo is what the home screen's HUD block shows. Set once at
// construction from the live settings and host; Server and System fields are
// refreshed by the root as background probes complete.
type BootInfo struct {
	Model        string // active chat-role model label
	Provider     string // provider name serving it
	Ready        bool   // false when no API key / nothing configured
	Version      string // "DeepThought <build>"
	Server       string // "Standalone — local settings only" / "Connected as …"
	Host         string
	SystemID     string // stable system UUID / fingerprint
	Cluster      string // cluster/site label (e.g. "vulcan (alliance)")
	Capabilities string // detected capabilities (e.g. "Slurm · Lmod · CVMFS · Apptainer")
	Project      string // active workspace project name
	Cwd          string
}

// SplashModel is the arcade start screen: big "DON'T PANIC" wordmark,
// DeepThought title banner, system/loadout HUD, and 1P/2P start menu.
// It is a plain struct, not a tea.Model — the root wraps it.
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

// WithSystem updates the system identity, cluster, and capability badges on the HUD.
func (m SplashModel) WithSystem(hostName, systemID, cluster, capabilities string) SplashModel {
	if hostName != "" {
		m.boot.Host = hostName
	}
	if systemID != "" {
		m.boot.SystemID = systemID
	}
	if cluster != "" {
		m.boot.Cluster = cluster
	}
	if capabilities != "" {
		m.boot.Capabilities = capabilities
	}
	return m
}

// WithProject updates the active project label on the HUD.
func (m SplashModel) WithProject(project string) SplashModel {
	m.boot.Project = project
	return m
}

// WithModel updates the active model and provider on the HUD.
func (m SplashModel) WithModel(model, provider string, ready bool) SplashModel {
	m.boot.Model = model
	m.boot.Provider = provider
	m.boot.Ready = ready
	return m
}

// Init kicks the spinner (its Update reschedules later ticks). The splash stays
// until the user explicitly selects an action (pass --no-splash to skip it).
func (m SplashModel) Init() tea.Cmd {
	return func() tea.Msg { return spinner.TickMsg{} } // first spin frame, now
}

// Update returns the concrete SplashModel type. Only Enter triggers an action;
// arrow keys, Tab, j/k, or 1-5 move the arcade selector cursor.
func (m SplashModel) Update(msg tea.Msg) (SplashModel, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		m.spinFrame++
		return m, cmd
	case tea.KeyPressMsg:
		switch msg.String() {
		case "down", "tab", "j":
			m.selected = (m.selected + 1) % 5
			m.notice = ""
		case "up", "shift+tab", "k":
			m.selected = (m.selected + 4) % 5
			m.notice = ""
		case "1":
			m.selected = 0
			m.notice = ""
		case "2":
			m.selected = 1
			m.notice = ""
		case "3":
			m.selected = 2
			m.notice = ""
		case "4":
			m.selected = 3
			m.notice = ""
		case "5":
			m.selected = 4
			m.notice = ""
		case "enter":
			switch m.selected {
			case 0:
				return m, func() tea.Msg { return SplashAdvanceMsg{} }
			case 1:
				return m, func() tea.Msg { return SplashServerLoginMsg{} }
			case 2:
				return m, func() tea.Msg { return WorkspaceAction{Kind: "discover"} }
			case 3:
				return m, func() tea.Msg { return WorkspaceAction{Kind: "menu"} }
			case 4:
				return m, Goto(ScreenStatus)
			}
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

// View composes the arcade start screen as one centered block. Below 90 columns
// or 24 rows it goes compact so it never overflows small terminals.
func (m SplashModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	compact := m.width < 90 || m.height < 24
	choices := []string{
		"  [1P] SINGLE PLAYER  · Run standalone (local terminal & cluster)",
		"  [2P] MULTIPLAYER    · Log in to server (browser link / SQL sync / fleet)",
		"  [++] AI LOADOUT     · Set up AI providers & models",
		"  [>>] COMMAND DECK   · Explore / Settings / Resume chat / Projects",
		"  [##] SYSTEM CABINET · Multi-cluster status, jobs & diagnostics",
	}
	if compact {
		choices = []string{
			"  [1P] Run standalone",
			"  [2P] Log in to server",
			"  [++] Set up AI providers",
			"  [>>] Explore / Settings",
			"  [##] System status",
		}
	}
	for i := range choices {
		choices[i] = clipLine(choices[i], max(12, m.width-4))
	}
	choices[m.selected] = clipLine("▶ "+strings.TrimSpace(choices[m.selected]), max(12, m.width-4))

	var rows []string
	if compact {
		headerLine := clipLine("DON'T PANIC · "+m.version(), max(10, m.width-2))
		rows = append(rows, styleName.Render(headerLine))
	} else {
		rows = append(rows,
			wordmarkHeader(m.width, max(4, m.height-20)),
			"",
			styleName.Render(clipLine(splashTagline, max(10, m.width-4))),
			styleVersion.Render(clipLine(m.version()+" · SELECT GAME MODE", max(10, m.width-4))),
		)
	}
	rows = append(rows, "", m.infoBlock(compact), "")
	for i, c := range choices {
		if i == m.selected {
			rows = append(rows, styleName.Render(c))
		} else {
			rows = append(rows, styleVersion.Render(c))
		}
	}
	if m.notice != "" {
		rows = append(rows, styleVersion.Render(clipLine(m.notice, max(10, m.width-4))))
	} else {
		rows = append(rows, "")
	}
	rows = append(rows, styleVersion.Render(clipLine(splashHint, max(10, m.width-4))))
	block := lipgloss.JoinVertical(lipgloss.Center, rows...)
	return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(placeCenter(m.width, m.height, block))
}

func (m SplashModel) version() string {
	return orDefault(m.boot.Version, "DeepThought (development build)")
}

// infoBlock is the left-aligned HUD label/value block under the tagline.
func (m SplashModel) infoBlock(compact bool) string {
	maxVal := max(10, m.width-14)
	row := func(label, value string) string {
		return styleSettingsKey.Render(fmt.Sprintf("%-8s", label)) + " " + styleVersion.Render(clipLine(value, maxVal))
	}
	rows := []string{row("loadout", m.statusLine())}
	if !compact {
		hostLine := orDefault(m.boot.Host, "unknown")
		if m.boot.Cluster != "" {
			hostLine = m.boot.Cluster + " · " + hostLine
		}
		if m.boot.SystemID != "" {
			shortID := m.boot.SystemID
			if len(shortID) > 12 {
				shortID = shortID[:12]
			}
			hostLine += " [" + shortID + "]"
		}
		if m.boot.Capabilities != "" {
			hostLine += " · " + m.boot.Capabilities
		}
		cwdLine := orDefault(m.boot.Cwd, "unknown")
		if m.boot.Project != "" {
			cwdLine = m.boot.Project + " (" + cwdLine + ")"
		}
		rows = append(rows,
			row("network", orDefault(m.boot.Server, "Standalone — local settings only")),
			row("system", hostLine),
			row("project", cwdLine),
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

// wordmarkHeader picks the biggest "DON'T PANIC" wordmark that fits
// width×height (standard font, stacked standard, then small), painted along
// the brand gradient; plain styled text when no art fits.
func wordmarkHeader(width, height int) string {
	for _, cand := range [][]string{
		bigTextIn("standard", "DON'T PANIC"),
		stackArt(bigTextIn("standard", "DON'T"), bigTextIn("standard", "PANIC")),
		bigTextIn(smallTextFont, "DON'T PANIC"),
	} {
		if len(cand) > 0 && widestRow(cand) <= width && len(cand) <= height {
			return paintGradient(cand)
		}
	}
	fallback := "DON'T PANIC · DeepThought"
	if len(fallback) > width && width > 0 {
		fallback = "DeepThought"
		if len(fallback) > width {
			fallback = fallback[:width]
		}
	}
	return styleName.Render(fallback)
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
