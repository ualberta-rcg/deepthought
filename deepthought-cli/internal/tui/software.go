package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/cvmfs"
)

type softwarePhase int

const (
	phaseSearch    softwarePhase = iota // editing the search box / idle / not-found
	phaseSearching                      // a `module spider` query is in flight
	phaseResults                        // versions listed, cursor over them
	phaseDetail                         // load prerequisites for a chosen version
)

// SoftwareModel is the F11 Software screen: a searchable view of the cluster's
// Lmod/CVMFS module tree, in the vulcan-status reference-block idiom. The search
// box is always focused; Enter runs `module spider <query>` in a background Cmd
// (the UI never blocks on CVMFS), the results list the versions, and selecting
// one runs `module spider <name>/<ver>` to show the exact load line. The static
// reference blocks (common stacks, CVMFS roots, notes) mirror the alliance-cvmfs
// skill. Plain struct, not a tea.Model.
type SoftwareModel struct {
	input      textinput.Model
	client     *cvmfs.Client
	detected   bool
	phase      softwarePhase
	haveResult bool
	query      string
	result     cvmfs.SpiderResult
	versionSel int
	detail     cvmfs.SpiderDetail
	vp         viewport.Model
	width      int
	height     int
}

// NewSoftwareModel builds the screen. `cvmfs.Detected()` is probed once here.
func NewSoftwareModel() SoftwareModel {
	ti := textinput.New()
	ti.Prompt = "⌕ "
	ti.Placeholder = "search modules… (e.g. cuda, python, openmpi)"
	ti.CharLimit = 200
	ti.Focus() // Focus() is a pointer receiver — set it on the stored input, else it
	// is a no-op and the box drops every keypress (textinput ignores keys when
	// !Focused). Mirrors NewChatModel.
	return SoftwareModel{
		input:    ti,
		client:   cvmfs.NewClient(nil),
		detected: cvmfs.Detected(),
		phase:    phaseSearch,
		vp:       viewport.New(),
	}
}

func (m SoftwareModel) Init() tea.Cmd { return m.input.Focus() }

func (m SoftwareModel) Resize(w, h int) SoftwareModel {
	m.width, m.height = w, h
	frameW := w - 4        // total frame width (matches the other AppScreen frames)
	contentW := frameW - 4 // inside the frame's border + padding
	if contentW < 10 {
		contentW = 10
	}
	m.input.SetWidth(contentW - 4) // single-line search field (input renders ~3 cols wider)
	// Frame border (2) + title (1) + search row (1) + keybar (1).
	bodyH := h - 2 - 3
	if bodyH < 1 {
		bodyH = 1
	}
	m.vp.SetWidth(contentW)
	m.vp.SetHeight(bodyH)
	return m
}

// --- async messages ---------------------------------------------------------

type spiderDoneMsg struct {
	query string
	res   cvmfs.SpiderResult
	err   string
}

type spiderDetailDoneMsg struct {
	full string
	res  cvmfs.SpiderDetail
	err  string
}

// searchCmd runs `module spider <query>` in a goroutine (tea.Cmd) and emits a
// spiderDoneMsg. CVMFS can be slow on a cold cache, so this never runs inline.
func (m SoftwareModel) searchCmd() tea.Cmd {
	query := strings.TrimSpace(m.input.Value())
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := client.Spider(ctx, query)
		return spiderDoneMsg{query: query, res: res, err: errText(err)}
	}
}

// detailCmd runs `module spider <name>/<ver>` for the chosen version.
func (m SoftwareModel) detailCmd(full string) tea.Cmd {
	name, ver := splitModVer(full)
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := client.SpiderDetail(ctx, name, ver)
		return spiderDetailDoneMsg{full: full, res: res, err: errText(err)}
	}
}

func (m SoftwareModel) Update(msg tea.Msg) (SoftwareModel, tea.Cmd) {
	switch msg := msg.(type) {
	case spiderDoneMsg:
		m.haveResult = true
		m.query = msg.query
		if msg.err == "" {
			m.result = msg.res
		}
		if m.result.Found && len(m.result.Versions) > 0 {
			m.phase, m.versionSel = phaseResults, 0
		} else {
			m.phase = phaseSearch // not-found / matches, with the reference below
		}
		return m, nil
	case spiderDetailDoneMsg:
		if msg.err == "" {
			m.detail = msg.res
		}
		if m.detail.Found {
			m.phase = phaseDetail
		}
		return m, nil
	}

	if kp, ok := msg.(tea.KeyPressMsg); ok {
		if kp.String() == "esc" {
			return m, Back()
		}
		if kp.String() == "enter" {
			switch m.phase {
			case phaseResults:
				if m.versionSel < len(m.result.Versions) {
					m.detail = cvmfs.SpiderDetail{}
					return m, m.detailCmd(m.result.Versions[m.versionSel])
				}
			case phaseSearch:
				if q := strings.TrimSpace(m.input.Value()); q != "" {
					m.query, m.phase = q, phaseSearching
					m.haveResult = false
					m.result = cvmfs.SpiderResult{}
					m.detail = cvmfs.SpiderDetail{}
					return m, m.searchCmd()
				}
			}
			return m, nil
		}
		// ↑/↓ (or j/k) move the version cursor while results are shown.
		if m.phase == phaseResults {
			switch kp.String() {
			case "up", "k":
				if m.versionSel > 0 {
					m.versionSel--
				}
				return m, nil
			case "down", "j":
				if m.versionSel < len(m.result.Versions)-1 {
					m.versionSel++
				}
				return m, nil
			}
		}
	}

	// Everything else feeds the search box. Typing into it abandons any
	// results/detail and starts a fresh search (except while one is in flight).
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before && (m.phase == phaseResults || m.phase == phaseDetail) {
		m.phase = phaseSearch
		m.haveResult = false
		m.result = cvmfs.SpiderResult{}
		m.detail = cvmfs.SpiderDetail{}
	}
	return m, cmd
}

func (m SoftwareModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	frameW := m.width - 4
	contentW := frameW - 4
	if contentW < 10 {
		return ""
	}
	bodyH := m.height - 2 - 3 // title(1) + search row(1) + keybar(1), inside the border
	if bodyH < 1 {
		bodyH = 1
	}
	m.vp.SetContent(strings.Join(m.body(), "\n"))
	title := padLines(styleSettingsTitle.Render("DeepThought › Software"), contentW)
	searchRow := padLines(m.input.View(), contentW)
	block := padBlock(m.vp.View(), contentW, bodyH)
	out := title + "\n" + searchRow + "\n" + block + "\n" + padLines(m.keybar(), contentW)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colPrimary).
		Padding(0, 1).
		Width(frameW)
	return box.Render(out)
}

func (m SoftwareModel) keybar() string {
	switch m.phase {
	case phaseResults:
		return KeyBar([]KeyHint{
			{Key: "↑/↓", Label: "pick"},
			{Key: "Enter", Label: "how to load"},
			{Key: "type", Label: "new search"},
			{Key: "esc", Label: "back"},
		})
	case phaseDetail:
		return KeyBar([]KeyHint{
			{Key: "type", Label: "new search"},
			{Key: "esc", Label: "back"},
		})
	default:
		return KeyBar([]KeyHint{
			{Key: "Enter", Label: "search"},
			{Key: "esc", Label: "back"},
		})
	}
}

// --- body content -----------------------------------------------------------

func (m SoftwareModel) body() []string {
	if !m.detected {
		return []string{"", dimNote("  Modules (Lmod) not detected on this host — nothing to search.")}
	}
	switch m.phase {
	case phaseSearching:
		return concatRows([]string{"", dimNote("  searching for \"" + m.query + "\"…"), ""}, m.referenceRows())
	case phaseDetail:
		return append([]string{}, m.detailRows()...)
	case phaseResults:
		return append([]string{}, m.resultRows()...)
	default: // phaseSearch
		if m.haveResult {
			return concatRows(m.resultRows(), m.referenceRows())
		}
		return concatRows([]string{dimNote("  Type a module name above and press Enter to search the full CVMFS tree.")}, m.referenceRows())
	}
}

// resultRows renders the search result: a version list (with cursor) when the
// module is found, or a not-found + "did you mean" note otherwise.
func (m SoftwareModel) resultRows() []string {
	if m.result.Found && len(m.result.Versions) > 0 {
		name := m.result.Name
		if name == "" {
			name = m.query
		}
		rows := []string{sectionHead(name, fmt.Sprintf("%d versions", len(m.result.Versions)))}
		for i, v := range m.result.Versions {
			prefix := "  "
			if i == m.versionSel && m.phase == phaseResults {
				prefix = styleSettingsVal.Render("▶ ")
			}
			rows = append(rows, prefix+truncatePad(v, 24))
		}
		rows = append(rows, dimNote("  ↑/↓ to pick · Enter for the exact load line"))
		rows = append(rows, hintNote("module spider "+name))
		return append(rows, "")
	}
	rows := []string{}
	if m.query != "" {
		rows = append(rows, dimNote(fmt.Sprintf("  no module matches \"%s\"", m.query)))
	}
	if len(m.result.Matches) > 0 {
		rows = append(rows, dimNote("  did you mean: "+strings.Join(m.result.Matches, "  ")))
	}
	if len(rows) == 0 {
		rows = append(rows, dimNote("  (no results)"))
	}
	return append(rows, "")
}

func (m SoftwareModel) detailRows() []string {
	rows := []string{sectionHead("How to load " + m.detail.Full)}
	if len(m.detail.LoadLines) > 0 {
		rows = append(rows, dimNote("  load any ONE complete line below (don't mix lines):"))
		for _, line := range m.detail.LoadLines {
			rows = append(rows, "    "+styleSettingsVal.Render(strings.Join(line, "  ")))
		}
	} else {
		rows = append(rows, dimNote("  no extra prerequisites — load it directly."))
	}
	rows = append(rows, "")
	rows = append(rows, styleSettingsFoot.Render("  copy / paste:"))
	rows = append(rows, "  "+styleToolResult.Render(m.detail.LoadCmd))
	rows = append(rows, hintNote("module spider "+m.detail.Full))
	return rows
}

// referenceRows is the static alliance-cvmfs reference the screen always offers.
func (m SoftwareModel) referenceRows() []string {
	rows := []string{sectionHead("Common stacks", "fill <ver> from spider — never guess")}
	for _, cs := range commonStacks {
		rows = append(rows, "  "+truncatePad(cs.label, 16)+styleSettingsVal.Render(cs.load))
	}
	rows = append(rows, "")
	rows = append(rows, sectionHead("CVMFS roots"))
	for _, r := range cvmfsRoots {
		rows = append(rows, "  "+truncatePad(r.path, 36)+dimNote(r.note))
	}
	rows = append(rows, "")
	rows = append(rows, sectionHead("Notes"))
	for _, n := range cvmfsNotes {
		rows = append(rows, dimNote("  "+n))
	}
	return append(rows, "")
}

var commonStacks = []struct{ label, load string }{
	{"Python only", "StdEnv/2023 python/<ver>"},
	{"ML / deep learning", "StdEnv/2023 gcc/<ver> cuda/<ver> python/<ver>"},
	{"+ cuDNN", "StdEnv/2023 gcc/<ver> cuda/<ver> cudnn/<ver>"},
	{"MPI", "StdEnv/2023 gcc/<ver> openmpi/<ver>"},
	{"GPU-aware MPI", "StdEnv/2023 gcc/<ver> cuda/<ver> openmpi/<ver>"},
	{"Containers", "apptainer"},
}

var cvmfsRoots = []struct{ path, note string }{
	{"/cvmfs/soft.computecanada.ca", "standard software"},
	{"/cvmfs/restricted.computecanada.ca", "licensed (MATLAB, …)"},
	{"/cvmfs/containers.computecanada.ca", "apptainer images"},
}

var cvmfsNotes = []string{
	"• module avail is tier-incomplete — use `module spider` for discovery.",
	"• StdEnv/2023 is sticky; `module --force purge` for a full reset.",
	"• Python venvs go on $SCRATCH, built against one Python module.",
	"• `avail_wheels <pkg>` checks the cluster wheelhouse before pip.",
	"• in job scripts, load modules explicitly (submit-shell state isn't inherited).",
}

// --- helpers ----------------------------------------------------------------

// splitModVer splits "cuda/13.2" into ("cuda", "13.2"). A bare name has ver "".
func splitModVer(full string) (name, ver string) {
	if i := strings.LastIndex(full, "/"); i > 0 {
		return full[:i], full[i+1:]
	}
	return full, ""
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// concatRows returns a fresh slice with a's rows followed by b's (no shared backing).
func concatRows(a, b []string) []string { return append(append([]string{}, a...), b...) }
