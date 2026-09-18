package tui

import (
	"fmt"
	"runtime"
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
	CVMFS, Module, Slurm  bool
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

// SetSession refreshes this-session usage totals (for the Usage section).
func (m StatusModel) SetSession(in, out, lastContext, cycles, messages int) StatusModel {
	m.sessionIn, m.sessionOut, m.lastContext = in, out, lastContext
	m.cycles, m.messages = cycles, messages
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

// statusSection is one detection-gated block on the Status page. The page is the
// ordered set of these — add a new section by appending one entry to
// statusSections; the page grows, no new screen. Each rows func renders its own
// header + body.
type statusSection struct {
	show func(StatusModel) bool
	rows func(StatusModel) []string
}

func always(m StatusModel) bool  { return true }
func slurmUp(m StatusModel) bool { return m.env.Slurm && m.gathered }

func statusSections() []statusSection {
	return []statusSection{
		{always, StatusModel.sessionRows},
		{always, StatusModel.nodeRows},
		{always, StatusModel.providersRows},
		{always, StatusModel.modelsRows},
		{always, StatusModel.usageRows},
		{always, StatusModel.toolsRows},
		{slurmUp, StatusModel.slurmClusterRows},
		{slurmUp, StatusModel.slurmJobsRows},
		{func(m StatusModel) bool { return slurmUp(m) && len(m.cluster.FairshareRows) > 0 }, StatusModel.slurmFairshareRows},
		{func(m StatusModel) bool { return len(m.cluster.StorageRows) > 0 }, StatusModel.slurmDiskRows},
	}
}

func (m StatusModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	var rows []string
	for _, sec := range statusSections() {
		if !sec.show(m) {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, trimTrailingBlanks(sec.rows(m))...)
	}
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

// nodeRows: the login node you're connected to, plus what it detected.
func (m StatusModel) nodeRows() []string {
	e := m.env
	yesno := func(b bool) string {
		if b {
			return styleToolResult.Render("✓")
		}
		return styleError.Render("✗")
	}
	return []string{
		styleSettingsTitle.Render("Login node"),
		kv("host", orDefault(e.Host, "?")),
		kv("user", orDefault(e.User, "?")),
		kv("shell", orDefault(e.Shell, "?")),
		kv("os", runtime.GOOS+"/"+runtime.GOARCH),
		kv("tz", orDefault(e.TZ, "?")),
		styleSettingsFoot.Render(fmt.Sprintf("  cvmfs %s · module %s · slurm %s",
			yesno(e.CVMFS), yesno(e.Module), yesno(e.Slurm))),
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

// usageRows: this-session token use + per-model lifetime totals (ex-F8 Stats).
func (m StatusModel) usageRows() []string {
	rows := []string{
		styleSettingsTitle.Render("Usage"),
		kv("input", fmt.Sprintf("%s tokens", formatTokens(m.sessionIn))),
		kv("output", fmt.Sprintf("%s tokens", formatTokens(m.sessionOut))),
		kv("total", fmt.Sprintf("%s tokens", formatTokens(m.sessionIn+m.sessionOut))),
		kv("context", fmt.Sprintf("%s (last turn)", formatTokens(m.lastContext))),
		kv("rounds", fmt.Sprintf("%d LLM · %d msgs", m.cycles, m.messages)),
		kv("est. cost", estSessionCost(m.sessionIn, m.sessionOut)),
	}
	var usage map[string]history.Cost
	if m.usage != nil {
		usage = m.usage()
	}
	if len(usage) == 0 {
		rows = append(rows, styleSettingsFoot.Render("  lifetime: (no recorded usage yet)"))
	} else {
		rows = append(rows, styleSettingsFoot.Render("  lifetime (all chats):"))
		for id, c := range usage {
			rows = append(rows, fmt.Sprintf("    %s  %s",
				truncatePad(id, 26),
				styleSettingsFoot.Render(fmt.Sprintf("in %s · out %s",
					formatTokens(c.InputTokens), formatTokens(c.OutputTokens)))))
		}
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

// The Slurm sections (ex-F10 Cluster screen) — shown only when Slurm is detected
// and the snapshot has gathered. They reuse the vulcan-status-style block
// renderers in cluster.go.
func (m StatusModel) slurmClusterRows() []string   { return renderClusterBlock(m.cluster) }
func (m StatusModel) slurmJobsRows() []string      { return renderJobsBlock(m.cluster) }
func (m StatusModel) slurmFairshareRows() []string { return renderFairshareBlock(m.cluster) }
func (m StatusModel) slurmDiskRows() []string      { return renderStorageBlock(m.cluster) }

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

// trimTrailingBlanks removes trailing empty rows (block renderers add one for
// their own spacing; the page adds its own separator between sections).
func trimTrailingBlanks(rows []string) []string {
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
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
