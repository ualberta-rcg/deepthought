package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/credential"
	"deepthought-cli/internal/host"
	"deepthought-cli/internal/keybindings"
	"deepthought-cli/internal/tools"
	"deepthought-cli/internal/tui"
)

type startupMsg struct{}
type candidatesMsg struct {
	Candidates []config.Candidate
	Auto, Skip bool
}
type hostMsg struct{ Record host.Record }
type hostTimerMsg struct{}
type providerSavedMsg struct {
	Name string
	Err  error
}
type toolsReadyMsg struct {
	Tools []tools.Tool
	Err   error
}

func (m RootModel) localStore() *config.LocalStore {
	if m.deps.Live == nil {
		return nil
	}
	return m.deps.Live.LocalStore()
}
func (m *RootModel) showWorkspace(title, notice string, items []tui.WorkspaceItem) {
	m.workspace = tui.WorkspaceModel{Title: title, Notice: notice, Items: items, Width: m.width, Height: m.height}
	m.pushScreenOnce(tui.ScreenWorkspace)
}
func (m *RootModel) showMenu() {
	items := []tui.WorkspaceItem{
		{Label: "Continue working", Kind: "home"}, {Label: "Settings", Kind: "action", ID: string(keybindings.Settings)},
		{Label: "Discover AI providers", Detail: "Review credentials and endpoints before connecting", Kind: "discover"},
		{Label: "Models and provider catalogs", Kind: "action", ID: string(keybindings.Models)},
		{Label: "Switch active model", Kind: "action", ID: string(keybindings.Model)},
		{Label: "Reasoning effort", Kind: "action", ID: string(keybindings.Effort)},
		{Label: "New chat", Kind: "action", ID: string(keybindings.NewChat)},
		{Label: "Continue a saved chat", Kind: "action", ID: string(keybindings.Resume)},
		{Label: "Context", Kind: "action", ID: string(keybindings.ContextView)},
		{Label: "Jobs", Kind: "screen", ID: strconv.Itoa(int(tui.ScreenJobs))},
		{Label: "Plans", Kind: "screen", ID: strconv.Itoa(int(tui.ScreenPlans))},
		{Label: "Cron", Kind: "action", ID: string(keybindings.Cron)},
		{Label: "Permission mode", Kind: "action", ID: string(keybindings.QueenMode)},
		{Label: "Sidebar visibility", Kind: "action", ID: string(keybindings.Sidebar)},
		{Label: "Status", Kind: "action", ID: string(keybindings.Diagnostics)},
		{Label: "Hosts and services", Kind: "hosts"},
		{Label: "Refresh scientific endpoints", Detail: "Connect to explicitly configured tool servers", Kind: "tools"},
		{Label: "Help / install on another machine", Kind: "help"},
	}
	m.showWorkspace("Navigation", "All function-key actions are available here.", items)
}
func (m *RootModel) showCandidates() {
	items := []tui.WorkspaceItem{}
	for _, c := range m.candidates {
		detail := c.Source + " · credential [masked]"
		if c.Untrusted {
			detail = "PROJECT INPUT — review endpoint · " + detail
		}
		items = append(items, tui.WorkspaceItem{Label: c.Name + " · " + c.URL, Detail: detail, Kind: "candidate", ID: c.ID})
	}
	for _, p := range m.deps.Live.Snapshot().Providers {
		if p.BaseURL == config.LegacyAlephURL {
			items = append(items, tui.WorkspaceItem{Label: "Migrate legacy Aleph URL: " + p.Name, Detail: config.DefaultBaseURL, Kind: "migrate-url", ID: p.Name})
		}
	}
	items = append(items, tui.WorkspaceItem{Label: "Enter configuration manually", Kind: "action", ID: string(keybindings.Settings)}, tui.WorkspaceItem{Label: "Retry discovery", Kind: "discover"}, tui.WorkspaceItem{Label: "Skip setup and start exploring", Kind: "skip-setup"})
	note := "Select a candidate to review. No provider connections have been made."
	if len(m.candidates) == 0 {
		note = "No keys found. Vulcan's ~/.aleph_tyk.env may take a few minutes to appear; retry or configure manually."
	}
	m.showWorkspace("Provider setup", note, items)
}
func (m RootModel) discoverCmd(auto bool) tea.Cmd {
	store := m.localStore()
	return func() tea.Msg {
		if auto && store != nil {
			id, _ := host.ID()
			var done bool
			if store.ReadRecord("setup", id, &done) == nil && done {
				return candidatesMsg{Auto: true, Skip: true}
			}
		}
		home, _ := os.UserHomeDir()
		cwd, _ := os.Getwd()
		return candidatesMsg{Candidates: config.Discover(config.DiscoveryOptions{Home: home, WorkDir: cwd}), Auto: auto}
	}
}
func (m RootModel) collectHostCmd() tea.Cmd {
	store := m.localStore()
	scheduler := m.lastCluster
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var s host.Store
		if store != nil {
			s = store
		}
		r := host.Collect(ctx, s)
		if !scheduler.FetchedAt.IsZero() {
			r.Services = append([]host.Service(nil), r.Services...)
			for i := range r.Services {
				service := &r.Services[i]
				if service.Kind != "Slurm" {
					continue
				}
				service.Source, service.LastSeen = "sinfo / user-scoped squeue", scheduler.FetchedAt
				service.Availability = "available"
				if scheduler.Err != nil || time.Since(scheduler.FetchedAt) > 15*time.Minute {
					service.Availability = "unavailable or stale"
				}
				if scheduler.JobsKnown {
					service.Capabilities = []string{"own job monitoring"}
				}
				if store != nil {
					_ = store.WriteRecord("service", service.ID, service)
				}
			}
		}
		return hostMsg{Record: r}
	}
}
func (m *RootModel) showHosts() {
	r := m.env.Observation
	items := []tui.WorkspaceItem{{Label: r.Summary(), Detail: "Observed " + r.CollectedAt.Format(time.RFC3339) + " · " + r.IdentitySource}, {Label: "Refresh host observations", Detail: "Cached for at least 30 seconds", Kind: "refresh-host"}, {Label: "Detailed status", Kind: "action", ID: string(keybindings.Diagnostics)}}
	for _, s := range r.Services {
		items = append(items, tui.WorkspaceItem{Label: s.Kind + ": " + s.Availability, Detail: "Source: " + s.Source + " · " + s.LastSeen.Format(time.RFC3339)})
	}
	if store := m.localStore(); store != nil {
		records, _ := store.Records("host")
		for _, raw := range records {
			var h host.Record
			if json.Unmarshal(raw, &h) == nil && h.ID != r.ID {
				items = append(items, tui.WorkspaceItem{Label: h.Name + " · remembered host", Detail: "Last seen " + h.LastSeen.Format(time.RFC3339)})
			}
		}
	}
	m.showWorkspace("Hosts & Services", "Installed commands do not prove that a service is usable.", items)
}

func (m RootModel) workspaceAction(a tui.WorkspaceAction) (tea.Model, tea.Cmd) {
	switch a.Kind {
	case "menu":
		m.showMenu()
	case "home":
		m.screen = tui.ScreenChat
		m.screenStack = nil
	case "action":
		return m.handleAction(keybindings.Action(a.ID))
	case "screen":
		n, _ := strconv.Atoi(a.ID)
		return m.update(tui.ScreenChangeMsg{To: tui.Screen(n)})
	case "discover":
		if m.discoveryBusy {
			return m, nil
		}
		m.discoveryBusy = true
		m.showWorkspace("Provider setup", "Looking for supported local configuration…", []tui.WorkspaceItem{{Label: "Skip setup", Kind: "skip-setup"}})
		return m, m.discoverCmd(false)
	case "candidate":
		for _, c := range m.candidates {
			if c.ID == a.ID {
				m.showWorkspace("Review provider", c.Source+" · credential [masked]", []tui.WorkspaceItem{{Label: "Accept and discover models", Detail: c.Name + " · " + c.URL + " · " + c.Wire, Kind: "accept-provider", ID: c.ID}, {Label: "Back to candidates", Kind: "candidates"}, {Label: "Skip", Kind: "skip-setup"}})
				break
			}
		}
	case "candidates":
		m.showCandidates()
	case "accept-provider":
		if m.discoveryBusy {
			return m, nil
		}
		for _, c := range m.candidates {
			if c.ID == a.ID {
				m.discoveryBusy = true
				live := m.deps.Live
				m.workspace.Notice = "Saving selected provider…"
				return m, func() tea.Msg {
					p, err := c.Adopt(live.Path())
					if err != nil {
						return providerSavedMsg{Err: fmt.Errorf("could not save credential")}
					}
					f := live.Snapshot()
					base := p.Name
					for n := 2; ; n++ {
						found := false
						for _, old := range f.Providers {
							if old.Name == p.Name {
								found = true
							}
						}
						if !found {
							break
						}
						p.Name = fmt.Sprintf("%s %d", base, n)
					}
					f.Providers = append(f.Providers, p)
					err = live.Save(f)
					if err == nil && live.LocalStore() != nil {
						id, _ := host.ID()
						err = live.LocalStore().WriteRecord("setup", id, true)
					}
					return providerSavedMsg{Name: p.Name, Err: err}
				}
			}
		}
	case "skip-setup":
		if s := m.localStore(); s != nil {
			id, _ := host.ID()
			if err := s.WriteRecord("setup", id, true); err != nil {
				m.workspace.Notice = "Could not remember setup choice"
				return m, nil
			}
		}
		m.screen = tui.ScreenChat
		m.screenStack = nil
		m.chat = m.chat.Notice("Setup skipped. Ctrl+P → Discover AI providers whenever you are ready.")
	case "migrate-url":
		f := m.deps.Live.Snapshot()
		for i := range f.Providers {
			if f.Providers[i].Name == a.ID && f.Providers[i].BaseURL == config.LegacyAlephURL {
				f.Providers[i].BaseURL = config.DefaultBaseURL
			}
		}
		if err := m.deps.Live.Save(f); err != nil {
			m.workspace.Notice = credential.Redact(err.Error())
		} else {
			m.showCandidates()
		}
	case "hosts":
		m.showHosts()
	case "refresh-host":
		return m, m.collectHostCmd()
	case "help":
		m.showWorkspace("Help", "Ctrl+P opens navigation; Settings contains configuration and shortcuts.", []tui.WorkspaceItem{{Label: "Install on another machine (copy only)", Detail: tui.InstallCommand, Kind: "install"}, {Label: "Keyboard shortcuts", Kind: "settings-tab", ID: "keyboard"}, {Label: "Configure providers", Kind: "discover"}, {Label: "Open Settings", Kind: "action", ID: string(keybindings.Settings)}})
	case "install":
		m.showWorkspace("Install on another machine", "Copy only; DeepThought never executes this command.", []tui.WorkspaceItem{{Label: "Copy full install command", Detail: tui.InstallCommand, Kind: "copy-install"}, {Label: "Full command is also in README.md and docs/SETUP.md"}})
	case "copy-install":
		m.workspace.Notice = "Clipboard copy requested (requires terminal clipboard support)."
		return m, tea.SetClipboard(tui.InstallCommand)
	case "settings-tab":
		m.settings = tui.NewSettingsModelAt(m.deps.Live, m.deps.Settings, a.ID, false).SetEnv(m.env).Resize(m.width, m.height)
		m.pushScreenOnce(tui.ScreenSettings)
	case "tools":
		if m.deps.DiscoverTools == nil {
			return m, nil
		}
		discover := m.deps.DiscoverTools
		m.workspace.Notice = "Refreshing configured scientific endpoints…"
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			ts, err := discover(ctx)
			return toolsReadyMsg{Tools: ts, Err: err}
		}
	case "export-settings":
		f := config.Portable(m.deps.Live.Snapshot())
		path := filepath.Join(filepath.Dir(m.deps.Live.Path()), "config.export.json")
		if err := config.Save(path, &f); err != nil {
			m.chat = m.chat.Notice("Export failed")
		} else {
			m.chat = m.chat.Notice("Portable settings exported to " + path)
		}
		m.screen = tui.ScreenChat
	case "import-settings":
		m.showWorkspace("Import settings", "Import replaces saved local settings; original file is retained. Credentials stay local.", []tui.WorkspaceItem{{Label: "Import " + m.deps.Live.Path(), Kind: "confirm-import"}, {Label: "Cancel", Kind: "home"}})
	case "confirm-import":
		cfg, err := config.Load(m.deps.Live.Path())
		if err == nil {
			cfg.Revision = m.deps.Live.Snapshot().Revision
			err = m.deps.Live.Save(cfg.File)
		}
		if err != nil {
			m.workspace.Notice = "Import failed; existing settings retained."
		} else {
			m.workspace.Notice = "Imported settings; Ctrl+P opens navigation."
		}
	}
	return m, nil
}

func (m *RootModel) refreshBindings() {
	if m.deps.Live == nil {
		return
	}
	bindings := map[string]keybindings.Action{}
	for k, v := range m.deps.Live.Snapshot().Keybindings {
		bindings[k] = keybindings.Action(v)
	}
	m.bindings = keybindings.WithOverrides(bindings)
}

func safeObservation(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}
