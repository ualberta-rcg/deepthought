package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/slurm"
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

// EnvInfo is the static machine/session environment gathered once at startup.
type EnvInfo struct {
	CVMFS, Module, Slurm bool
	Shell, Host, User, TZ string
}

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

// StatusModel is the unified F12 Status page: session (model/effort/thinking/
// mode/health/clock), providers (cached), models + token use, tools, environment,
// and cluster metrics (Slurm). All data is gathered in the background — opening
// the page never blocks. Plain struct, not a tea.Model.
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
	vp        viewport.Model
	width     int
	height    int
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
	return m
}

// SetHealth refreshes the model-reachability state.
func (m StatusModel) SetHealth(ok bool, reason string) StatusModel {
	m.healthOK, m.health = ok, reason
	return m
}

// SetClock stamps the live clock (date/timezone tick every second).
func (m StatusModel) SetClock(t time.Time) StatusModel { m.clock = t; return m }

// SetCluster fills the cached Slurm snapshot from the background poller.
func (m StatusModel) SetCluster(s slurm.ClusterSnapshot) StatusModel {
	m.cluster, m.gathered = s, true
	return m
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

func (m StatusModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	rows := append([]string{}, m.sessionRows()...)
	rows = append(rows, "")
	rows = append(rows, m.providersRows()...)
	rows = append(rows, "")
	rows = append(rows, m.modelsRows()...)
	rows = append(rows, "")
	rows = append(rows, m.toolsRows()...)
	rows = append(rows, "")
	rows = append(rows, m.envRows()...)
	rows = append(rows, "")
	rows = append(rows, m.clusterRows()...)

	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	m.vp.SetContent(body)
	keybar := KeyBar([]KeyHint{
		{Key: "↑/↓", Label: "scroll"},
		{Key: "PgUp/PgDn", Label: ""},
		{Key: "r", Label: "refresh"},
		{Key: "esc", Label: "back"},
	})
	return AppScreenScroll(m.width, m.height, "DeepThought › Status", m.vp.View(), m.vp.Height(), keybar)
}

// --- section renderers ------------------------------------------------------

func (m StatusModel) sessionRows() []string {
	snap := m.store.Snapshot()
	label, provider, caps := "—", "", ""
	if mm, ok := activeModel(snap); ok {
		label, provider, caps = mm.Label, mm.Provider, capabilitiesLabel(mm)
		if label == "" {
			label = mm.ID
		}
	}
	health := styleError.Render("✗ " + orDefault(m.health, "unavailable"))
	if m.healthOK {
		health = styleToolResult.Render("✓ reachable")
	}
	clockStr := "—"
	if !m.clock.IsZero() {
		clockStr = m.clock.Format("2006-01-02 15:04:05 MST")
	}
	return []string{
		styleSettingsTitle.Render("Session"),
		kv("model", fmt.Sprintf("%s  %s", label, styleSettingsFoot.Render(provider+"·"+caps))),
		kv("effort", EffortLabel(babel.Effort(orDefault(m.status.Effort, "medium")))),
		kv("mode", orDefault(m.status.Mode, "—")),
		kv("health", health),
		kv("now", clockStr),
	}
}

func (m StatusModel) providersRows() []string {
	rows := []string{styleSettingsTitle.Render("Providers")}
	if m.providers == nil {
		return append(rows, styleSettingsFoot.Render("(no providers)"))
	}
	ps := m.providers()
	if len(ps) == 0 {
		return append(rows, styleSettingsFoot.Render("(none configured)"))
	}
	for _, p := range ps {
		key := "✗"
		if p.KeySet {
			key = "✓"
		}
		state := p.State
		if state == "" {
			state = "idle"
		}
		dot := styleSettingsFoot.Render("·")
		rows = append(rows, fmt.Sprintf("  %s %s %s key%s %s tags[%s]",
			truncatePad(p.Name, 18), styleSettingsFoot.Render(p.Wire), dot, key, renderState(state), p.Tags))
	}
	return rows
}

func (m StatusModel) modelsRows() []string {
	rows := []string{styleSettingsTitle.Render("Models")}
	if m.store == nil {
		return rows
	}
	snap := m.store.Snapshot()
	if len(snap.Models) == 0 {
		return append(rows, styleSettingsFoot.Render("(none configured)"))
	}
	var usage map[string]history.Cost
	if m.usage != nil {
		usage = m.usage()
	}
	for _, mm := range snap.Models {
		label := mm.Label
		if label == "" {
			label = mm.ID
		}
		rows = append(rows, fmt.Sprintf("  %s  %s  %s  %s",
			truncatePad(label, 24),
			truncatePad(mm.Provider, 12),
			truncatePad(capabilitiesLabel(mm), 18),
			styleSettingsFoot.Render(tokenUsage(mm, usage))))
	}
	return rows
}

func (m StatusModel) toolsRows() []string {
	rows := []string{styleSettingsTitle.Render("Tools")}
	if len(m.tools) == 0 {
		return append(rows, styleSettingsFoot.Render("(none)"))
	}
	return append(rows, "  "+styleSettingsVal.Render(strings.Join(m.tools, " · ")))
}

func (m StatusModel) envRows() []string {
	e := m.env
	yesno := func(b bool) string {
		if b {
			return styleToolResult.Render("✓")
		}
		return styleError.Render("✗")
	}
	return []string{
		styleSettingsTitle.Render("Environment"),
		fmt.Sprintf("  cvmfs %s  module %s  slurm %s", yesno(e.CVMFS), yesno(e.Module), yesno(e.Slurm)),
		fmt.Sprintf("  %s", styleSettingsFoot.Render(fmt.Sprintf("host %s · user %s · shell %s · tz %s",
			orDefault(e.Host, "?"), orDefault(e.User, "?"), orDefault(e.Shell, "?"), orDefault(e.TZ, "?")))),
	}
}

func (m StatusModel) clusterRows() []string {
	if !m.env.Slurm {
		return []string{styleSettingsTitle.Render("Cluster"), styleSettingsFoot.Render("(Slurm not detected)")}
	}
	if !m.gathered {
		return []string{styleSettingsTitle.Render("Cluster"), styleSettingsFoot.Render("gathering…")}
	}
	c := m.cluster
	if c.Err != nil {
		return []string{styleSettingsTitle.Render("Cluster"), styleError.Render("✗ " + c.Err.Error())}
	}
	nodesUp := c.NodesUp
	if nodesUp == 0 && c.NodesTotal > 0 {
		nodesUp = c.NodesTotal // fallback when %t parse yielded nothing
	}
	nodeFrac := 0.0
	if c.NodesTotal > 0 {
		nodeFrac = float64(nodesUp) / float64(c.NodesTotal)
	}
	cpuBar := bar(cpuFrac(c), 16)
	memFrac := 0.0
	if c.MemTotalGB > 0 {
		memFrac = float64(c.MemAllocGB) / float64(c.MemTotalGB)
		if memFrac > 1 {
			memFrac = 1
		}
	}
	memBar := bar(memFrac, 16)
	gpuFrac := 0.0
	if c.GPUs > 0 {
		gpuFrac = float64(c.GPUsUsed) / float64(c.GPUs)
		if gpuFrac > 1 {
			gpuFrac = 1
		}
	}
	gpuBar := bar(gpuFrac, 16)

	rows := []string{
		styleSettingsTitle.Render("Cluster"),
		fmt.Sprintf("  nodes  %s  %d/%d up", bar(nodeFrac, 16), nodesUp, c.NodesTotal),
		fmt.Sprintf("  cpus   %s  %d/%d  (%d%%)", cpuBar, c.CPUAlloc, c.CPUTotal, pct(cpuFrac(c))),
	}
	if c.MemTotalGB > 0 {
		rows = append(rows, fmt.Sprintf("  mem    %s  %d/%d GB  (%d%%)", memBar, c.MemAllocGB, c.MemTotalGB, pct(memFrac)))
	}
	if c.GPUs > 0 {
		gpuLabel := fmt.Sprintf("%d/%d", c.GPUsUsed, c.GPUs)
		if c.GPUType != "" {
			gpuLabel += " (" + c.GPUType + ")"
		}
		rows = append(rows, fmt.Sprintf("  gpus   %s  %s  (%d%%)", gpuBar, gpuLabel, pct(gpuFrac)))
	}
	rows = append(rows, fmt.Sprintf("  queue  running %d · pending %d", c.JobsRunning, c.JobsPending))
	if c.DefaultAccount != "" {
		fs := "—"
		if c.Fairshare > 0 {
			fs = fmt.Sprintf("%.3f", c.Fairshare)
		}
		rows = append(rows, fmt.Sprintf("  fairshare %s  %s", styleSettingsVal.Render(fs),
			styleSettingsFoot.Render("("+c.DefaultAccount+")")))
	}
	// Storage quotas (raw diskusage_report rows, already aligned).
	if len(c.Storage) > 0 {
		rows = append(rows, styleSettingsFoot.Render("  storage:"))
		for _, line := range c.Storage {
			rows = append(rows, "    "+line)
		}
	}
	// Your jobs (compact).
	switch {
	case len(c.YourJobs) == 0:
		rows = append(rows, "  your jobs "+styleSettingsFoot.Render("(none)"))
	default:
		rows = append(rows, styleSettingsFoot.Render("  your jobs (id state time name):"))
		for _, j := range c.YourJobs {
			rows = append(rows, fmt.Sprintf("    %s %s %s %s", truncatePad(j.ID, 12), truncatePad(j.State, 10), truncatePad(j.Elapsed, 8), j.Name))
		}
	}
	if !c.FetchedAt.IsZero() {
		rows = append(rows, styleSettingsFoot.Render("  updated "+c.FetchedAt.Format("15:04:05")))
	}
	return rows
}

func pct(f float64) int {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 100
	}
	return int(f*100 + 0.5)
}

// --- helpers ----------------------------------------------------------------

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

// kv renders a "label  value" row aligned with the rest of the page.
func kv(label, value string) string {
	return styleSettingsKey.Render(label) + "  " + value
}

func renderState(s string) string {
	switch s {
	case "ok":
		return styleToolResult.Render("ok")
	case "degraded":
		return styleError.Render("degraded")
	default:
		return styleSettingsFoot.Render("idle")
	}
}

func cpuFrac(c slurm.ClusterSnapshot) float64 {
	if c.CPUTotal <= 0 {
		return 0
	}
	f := float64(c.CPUAlloc) / float64(c.CPUTotal)
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f
}

// tokenUsage renders the recorded token total for one model, or "—" when none.
func tokenUsage(m unimatrix.Model, usage map[string]history.Cost) string {
	if usage == nil {
		return "tokens —"
	}
	c, ok := usage[m.ID]
	if !ok || (c.InputTokens == 0 && c.OutputTokens == 0) {
		return "tokens —"
	}
	return fmt.Sprintf("in %d · out %d", c.InputTokens, c.OutputTokens)
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
