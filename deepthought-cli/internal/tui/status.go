package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/host"
	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tui/kit"
	"deepthought-cli/internal/unimatrix"
)

// ProviderRow is one provider's cached status for the Status page. State is
// "ok" (used, breaker closed), "degraded" (breaker open), or "idle" (never
// used). No network is queried to build this.
type ProviderRow struct {
	Name, Wire, Tags string
	KeySet           bool
	State            string
}

// EnvInfo is the host/session descriptor gathered once at startup (the design
// doc's "one structured descriptor, many renderers": Status's Host section,
// the chat env brief, the sidebar, Settings › System all render this).
// Failed probes leave "" — never an error, never a crash.
type EnvInfo struct {
	Observation          host.Record
	CVMFS, Module, Slurm bool
	Shell, User, TZ      string

	// Host facts (pure-stdlib probes; see gatherEnv).
	Host      string // hostname as reported
	ShortName string // first label ("vulcan-login1")
	LongName  string // FQDN when known (may equal Host)
	OSName    string // PRETTY_NAME from /etc/os-release
	Kernel    string // /proc/sys/kernel/osrelease
	Arch      string // runtime.GOARCH (native builds = host arch)
	CPUs      int
	MemGB     int

	// Session identity for the home/status surfaces.
	Version string // "DeepThought <build>"
	Server  string // "standalone" or "server user @ host"
	Cwd     string // working directory, ~-shortened
}

func (m StatusModel) SetEnv(e EnvInfo) StatusModel { m.env = e; m.rebuild(); return m }

// StatusInputs bundles the Status page's external inputs (kept out of the model
// so the constructor stays readable).
type StatusInputs struct {
	Store     ConfigStore
	Status    StatusInfo
	HealthOK  bool
	Health    string
	Usage     func() map[string]history.Cost
	Providers func() []ProviderRow
	Tools     []string
	Env       EnvInfo
}

// StatusModel is the unified F12 Status page — an adaptive, detection-gated set
// of sections: session, login node, providers, models, usage, tools always;
// cluster / your jobs / fairshare when Slurm is detected; your dirs when disk
// usage is available. All data is gathered in the background — opening the page
// never blocks. Plain struct, not a tea.Model.
type StatusModel struct {
	store     ConfigStore
	status    StatusInfo
	healthOK  bool
	health    string
	usage     func() map[string]history.Cost
	providers func() []ProviderRow
	tools     []string
	env       EnvInfo
	clock     time.Time
	cluster   slurm.ClusterSnapshot
	gathered  bool // first Slurm snapshot has arrived
	// This-session usage totals (fed from the chat) for the Usage section.
	sessionIn, sessionOut, lastContext, cycles, messages int
	sessionEstIn, sessionEstOut                          int
	vp                                                   viewport.Model
	width                                                int
	height                                               int
}

// NewStatusModel builds the page from in. The Slurm snapshot starts empty and is
// filled by the root's background poller via SetCluster.
func NewStatusModel(in StatusInputs) StatusModel {
	return StatusModel{
		store:     in.Store,
		status:    in.Status,
		healthOK:  in.HealthOK,
		health:    in.Health,
		usage:     in.Usage,
		providers: in.Providers,
		tools:     in.Tools,
		env:       in.Env,
		vp:        viewport.New(),
	}
}

func (m StatusModel) Init() tea.Cmd { return nil }

func (m StatusModel) Resize(w, h int) StatusModel {
	m.width, m.height = w, h
	m.vp.SetWidth(w - 4) // inside the AppScreen border + pad
	bodyH := h - 2 - 2   // inside border, minus title row + keybar row
	if bodyH < 1 {
		bodyH = 1
	}
	m.vp.SetHeight(bodyH)
	m.rebuild()
	return m
}

// SetHealth refreshes the model-reachability state.
func (m StatusModel) SetHealth(ok bool, reason string) StatusModel {
	m.healthOK, m.health = ok, reason
	m.rebuild()
	return m
}

// SetClock stamps the live clock (date/timezone tick every second). The clock
// renders in the title, so a tick never rebuilds the body or moves the scroll.
func (m StatusModel) SetClock(t time.Time) StatusModel { m.clock = t; return m }

// SetCluster fills the cached Slurm snapshot from the background poller.
func (m StatusModel) SetCluster(s slurm.ClusterSnapshot) StatusModel {
	m.cluster, m.gathered = s, true
	m.rebuild()
	return m
}

// SetSession refreshes this-session usage totals (for the Usage section).
func (m StatusModel) SetSession(in, out, lastContext, cycles, messages int) StatusModel {
	m.sessionIn, m.sessionOut, m.lastContext = in, out, lastContext
	m.cycles, m.messages = cycles, messages
	m.rebuild()
	return m
}

// SetSessionEstimate records how much of the session totals is estimated.
func (m StatusModel) SetSessionEstimate(estIn, estOut int) StatusModel {
	m.sessionEstIn, m.sessionEstOut = estIn, estOut
	m.rebuild()
	return m
}

// statusTwoColumnW is the inner width at which the cards flow into two columns.
const statusTwoColumnW = 116

// rebuild renders the page body into the viewport. It runs only when page
// data changes (never per frame), and SetContent keeps the scroll offset.
// Wide terminals get two columns; each card goes to the shorter one.
func (m *StatusModel) rebuild() {
	w := m.vp.Width()
	if w < 20 {
		return
	}
	colW, cols := w, 1
	if w >= statusTwoColumnW {
		colW, cols = (w-1)/2, 2
	}
	columns := make([][]string, cols)
	heights := make([]int, cols)
	for _, sec := range statusSections() {
		if !sec.show(*m) {
			continue
		}
		card := sec.section(*m).Card(colW)
		c := 0
		if cols == 2 && heights[1] < heights[0] {
			c = 1
		}
		columns[c] = append(columns[c], card)
		heights[c] += lipgloss.Height(card)
	}
	if cols == 1 {
		m.vp.SetContent(lipgloss.JoinVertical(lipgloss.Left, columns[0]...))
		return
	}
	left := lipgloss.JoinVertical(lipgloss.Left, columns[0]...)
	right := lipgloss.JoinVertical(lipgloss.Left, columns[1]...)
	m.vp.SetContent(lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(colW+1).Render(left), right))
}

func (m StatusModel) Update(msg tea.Msg) (StatusModel, tea.Cmd) {
	if kp, ok := msg.(tea.KeyPressMsg); ok {
		switch kp.String() {
		case "r", "R":
			return m, func() tea.Msg { return RefreshClusterMsg{} }
		case "esc", "left", "h", "q":
			return m, Back()
		case "up", "k", "pgup", "down", "j", "pgdown", "home", "end", "g", "G":
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	}
	// Mouse wheel / drag scroll.
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// RefreshClusterMsg asks the root to re-poll Slurm now (the `r` key).
type RefreshClusterMsg struct{}

// statusSection is one detection-gated block on the Status page. The page is the
// ordered set of these — add a new section by appending one entry to
// statusSections; the page grows, no new screen. Each renders as one card.
type statusSection struct {
	show    func(StatusModel) bool
	section func(StatusModel) Section
}

func always(m StatusModel) bool  { return true }
func slurmUp(m StatusModel) bool { return m.env.Slurm && m.gathered }

func statusSections() []statusSection {
	return []statusSection{
		{always, StatusModel.sessionRows},
		{always, StatusModel.hostRows},
		{always, StatusModel.providersRows},
		{always, StatusModel.modelsRows},
		{always, StatusModel.usageRows},
		{always, StatusModel.toolsRows},
		{slurmUp, StatusModel.slurmClusterRows},
		{slurmUp, StatusModel.slurmJobsRows},
		{func(m StatusModel) bool { return slurmUp(m) && len(m.cluster.FairshareRows) > 0 }, StatusModel.slurmFairshareRows},
		{func(m StatusModel) bool { return len(m.cluster.StorageRows) > 0 }, StatusModel.slurmDiskRows},
		{func(m StatusModel) bool { return len(m.cluster.Storage) > 0 }, func(m StatusModel) Section {
			return Section{Title: "Personal quota report", Rows: m.cluster.Storage, Source: "diskusage_report (cached up to 15 minutes)"}
		}},
	}
}

func (m StatusModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	keybar := kit.KeyBar([]kit.Key{
		{Key: "↑↓", Help: "scroll"},
		{Key: "PgUp/PgDn", Help: "page"},
		{Key: kit.Mnemonics.Refresh, Help: "refresh"},
		{Key: "esc", Help: "back"},
	}, m.width-4)
	title := screenTitle("Status")
	if !m.clock.IsZero() {
		title += styleSettingsFoot.Render("  " + m.clock.Format("2006-01-02 15:04:05 MST"))
	}
	return AppScreenScroll(m.width, m.height, title, m.vp.View(), m.vp.Height(), keybar)
}

// --- section renderers ------------------------------------------------------

func (m StatusModel) sessionRows() Section {
	label, provider, caps := "—", "", ""
	if m.store == nil {
		return Section{Title: "Session", Rows: []string{
			kv("model", "—"), kv("mode", orDefault(m.status.Mode, "—")),
		}}
	}
	snap := m.store.Snapshot()
	if mm, ok := activeModel(snap); ok {
		label, provider, caps = mm.Label, mm.Provider, capabilitiesLabel(mm)
		if label == "" {
			label = mm.ID
		}
	}
	return Section{
		Title: "Session",
		Rows: []string{
			kv("model", fmt.Sprintf("%s  %s", label, styleSettingsFoot.Render(provider+"·"+caps))),
			kv("effort", EffortLabel(babel.Effort(orDefault(m.status.Effort, "medium")))),
			kv("mode", orDefault(m.status.Mode, "—")),
			kv("health", healthChip(m.healthOK, m.health)),
			kv("server", orDefault(m.env.Server, "standalone")),
			kv("version", orDefault(m.env.Version, "development build")),
		},
	}
}

// hostRows: the machine you're on — the host descriptor rendered (this box
// may not be a login node; it's just the host).
func (m StatusModel) hostRows() Section {
	e := m.env
	rows := []string{
		kv("host", orDefault(e.ShortName, orDefault(e.Host, "?"))),
	}
	if e.LongName != "" && e.LongName != e.ShortName {
		rows = append(rows, kv("fqdn", e.LongName))
	}
	if e.OSName != "" {
		rows = append(rows, kv("os", e.OSName))
	}
	if e.Kernel != "" {
		rows = append(rows, kv("kernel", e.Kernel))
	}
	rows = append(rows, kv("arch", orDefault(e.Arch, "?")))
	if e.CPUs > 0 {
		spec := fmt.Sprintf("%d", e.CPUs)
		if e.MemGB > 0 {
			spec += fmt.Sprintf(" · %d GB", e.MemGB)
		}
		rows = append(rows, kv("cpus", spec))
	}
	rows = append(rows,
		kv("user", orDefault(e.User, "?")),
		kv("shell", orDefault(e.Shell, "?")),
		kv("tz", orDefault(e.TZ, "?")),
		styleSettingsFoot.Render(fmt.Sprintf("  cvmfs %s · module %s · slurm %s",
			detChip(e.CVMFS), detChip(e.Module), detChip(e.Slurm))),
	)
	rows = append(rows, observationRows(e)...)
	return Section{Title: "Host", Rows: rows}
}

func (m StatusModel) providersRows() Section {
	var body []string
	if m.providers != nil {
		for _, p := range m.providers() {
			key := kit.G().Cross
			if p.KeySet {
				key = kit.G().Check
			}
			dot := styleSettingsFoot.Render("·")
			body = append(body, fmt.Sprintf("  %s %s %s key%s %s tags[%s]",
				truncatePad(p.Name, 18), styleSettingsFoot.Render(p.Wire), dot, key, stateChip(orDefault(p.State, "idle")), p.Tags))
		}
	}
	if len(body) == 0 {
		body = []string{emptyRow("providers")}
	}
	return Section{Title: "Providers", Rows: body}
}

func (m StatusModel) modelsRows() Section {
	var body []string
	if m.store != nil {
		usage := m.usageMap()
		for _, mm := range m.store.Snapshot().Models {
			label := mm.Label
			if label == "" {
				label = mm.ID
			}
			body = append(body, fmt.Sprintf("  %s  %s  %s  %s",
				truncatePad(label, 24),
				truncatePad(mm.Provider, 12),
				truncatePad(capabilitiesLabel(mm), 18),
				styleSettingsFoot.Render(tokenUsage(mm, usage))))
		}
	}
	if len(body) == 0 {
		body = []string{emptyRow("models")}
	}
	return Section{Title: "Models", Rows: body}
}

// usageRows: this-session token use (with a context-usage meter) + per-model
// lifetime totals (ex-F8 Stats).
func (m StatusModel) usageRows() Section {
	body := []string{
		kv("context", contextMeter(m.lastContext, m.activeContextWindow())),
		kv("input", estimatedTokens(m.sessionIn, m.sessionEstIn)),
		kv("output", estimatedTokens(m.sessionOut, m.sessionEstOut)),
		kv("total", estimatedTokens(m.sessionIn+m.sessionOut, m.sessionEstIn+m.sessionEstOut)),
		kv("rounds", fmt.Sprintf("%d LLM · %d msgs", m.cycles, m.messages)),
		kv("est. cost", estSessionCost(m.sessionIn, m.sessionOut)),
	}
	usage := m.usageMap()
	if len(usage) == 0 {
		body = append(body, styleSettingsFoot.Render("  lifetime: (no recorded usage yet)"))
	} else {
		body = append(body, styleSettingsFoot.Render("  lifetime (all chats):"))
		ids := make([]string, 0, len(usage))
		for id := range usage {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			c := usage[id]
			body = append(body, fmt.Sprintf("    %s  %s",
				truncatePad(id, 26),
				styleSettingsFoot.Render(fmt.Sprintf("in %s · out %s",
					formatTokens(c.InputTokens), formatTokens(c.OutputTokens)))))
		}
	}
	return Section{Title: "Usage", Extra: "this session", Rows: body}
}

func (m StatusModel) toolsRows() Section {
	body := []string{"  " + styleSettingsVal.Render(strings.Join(m.tools, " · "))}
	if len(m.tools) == 0 {
		body = []string{emptyRow("tools")}
	}
	return Section{Title: "Tools", Rows: body}
}

// The Slurm sections (ex-F10 Cluster screen) — shown only when Slurm is detected
// and the snapshot has gathered. They reuse the vulcan-status-style block
// renderers in cluster.go.
func (m StatusModel) slurmClusterRows() Section   { return clusterSection(m.cluster) }
func (m StatusModel) slurmJobsRows() Section      { return jobsSection(m.cluster) }
func (m StatusModel) slurmFairshareRows() Section { return fairshareSection(m.cluster) }
func (m StatusModel) slurmDiskRows() Section      { return storageSection(m.cluster) }

// --- helpers ----------------------------------------------------------------

// estCost rates are rough $/MTok for a glance estimate on the Status page (not
// billing), tuned for a mid-tier open-weight gateway.
const (
	estCostPerMIn  = 0.15
	estCostPerMOut = 0.60
)

func estSessionCost(in, out int) string {
	if in+out == 0 {
		return "—"
	}
	usd := (float64(in)/1e6)*estCostPerMIn + (float64(out)/1e6)*estCostPerMOut
	if usd < 0.01 {
		return fmt.Sprintf("~$%.4f", usd)
	}
	return fmt.Sprintf("~$%.2f", usd)
}

// activeModel resolves the running (agentic, falling back to chat) model from a
// config snapshot.
func activeModel(snap config.File) (unimatrix.Model, bool) {
	role := snap.Roles[unimatrix.RoleAgentic]
	if role == "" {
		role = snap.Roles[unimatrix.RoleChat]
	}
	for _, mm := range snap.Models {
		if mm.ID == role {
			return mm, true
		}
	}
	return unimatrix.Model{}, false
}

// activeContextWindow is the active model's context window (tokens), or 0 when
// the active model is unknown or declares no window. Drives the Usage meter.
func (m StatusModel) activeContextWindow() int {
	if m.store == nil {
		return 0
	}
	if mm, ok := activeModel(m.store.Snapshot()); ok {
		return mm.Context
	}
	return 0
}

// usageMap returns the per-model lifetime usage, or nil when no provider is set.
func (m StatusModel) usageMap() map[string]history.Cost {
	if m.usage == nil {
		return nil
	}
	return m.usage()
}

// kv renders an aligned "label  value" row. The raw label is padded to
// statusLabelW *before* styling so values line up in a column; labels are short
// ASCII, so byte padding is safe and we never truncate them.
func kv(label, value string) string {
	return styleSettingsKey.Render(fmt.Sprintf("%-*s", statusLabelW, label)) + "  " + value
}

// tokenUsage renders the recorded token total for one model (same formatTokens
// style as the Usage section), or "—" when none.
func tokenUsage(m unimatrix.Model, usage map[string]history.Cost) string {
	if usage == nil {
		return "tokens —"
	}
	c, ok := usage[m.ID]
	if !ok || (c.InputTokens == 0 && c.OutputTokens == 0) {
		return "tokens —"
	}
	return fmt.Sprintf("in %s · out %s", formatTokens(c.InputTokens), formatTokens(c.OutputTokens))
}

func capabilitiesLabel(m unimatrix.Model) string {
	var caps []string
	if m.Agentic() {
		caps = append(caps, "agentic")
	}
	if m.Can(unimatrix.CapReasoning) {
		caps = append(caps, "reasoning")
	}
	if m.Can(unimatrix.CapVision) {
		caps = append(caps, "vision")
	}
	if len(caps) == 0 {
		return "chat"
	}
	return strings.Join(caps, "/")
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// estimatedTokens renders a session total, marking the estimated portion:
// "12.3k tokens" when measured, "~12.3k tokens (1.1k estimated)" otherwise.
func estimatedTokens(total, estimated int) string {
	if estimated <= 0 {
		return formatTokens(total) + " tokens"
	}
	return fmt.Sprintf("~%s tokens (%s estimated)", formatTokens(total), formatTokens(estimated))
}
