// Package app holds DeepThought's root model — the only type in the binary that
// satisfies tea.Model — and the Wish SSH handler that builds one per session.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/credential"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/keybindings"
	"deepthought-cli/internal/queen"
	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tools"
	"deepthought-cli/internal/tui"
	"deepthought-cli/internal/unimatrix"
	"deepthought-cli/internal/workflow"
)

type modelHealthMsg struct {
	ok     bool
	reason string
}

// statusCmdMsg carries cached stdout from an optional StatusLine.Command poll.
type statusCmdMsg struct{ out string }

// statusCmdInterval is how often the bottom-bar shell command is re-run.
const statusCmdInterval = 30 * time.Second

// Deps bundles the per-session inputs the root model needs: the top-bar status
// cluster, the shared inference client + active model id, the tool registry and
// Queen gate, and the screen to start on. Built once in main (the client/registry/
// gate pointers are shared, concurrency-safe); the SSH handler copies it per
// connection.
// Deps is everything the root model needs that comes from outside the TUI:
// the live settings handle (role resolution, pooled clients, the thinking
// toggle), the status-bar snapshot, the tool registry and Queen gate, and the
// screen to start on. Built once in main (the settings/registry/gate pointers
// are shared, concurrency-safe); the SSH handler copies it per connection.
type Deps struct {
	StartupNotice     string
	DiscoverTools     func(context.Context) ([]tools.Tool, error)
	Workflows         *workflow.Service
	SkillListing      func() string
	ReloadSkills      func() error
	Scheduler         *slurm.Client
	Context           context.Context
	Publish           func(RunnerFrame) error
	ResumeCollective  string
	DisableMonitoring bool
	Status            tui.StatusInfo
	Settings          tui.SettingsInfo
	Live              *Settings
	Registry          *tools.Registry
	Gate              *queen.Gate
	Skills            string                  // compact "available skills" index, injected into each chat request
	ChatSource        history.ChatStoreSource // a ChatStore per chat (FileStore now; SQLite after 3b)
	StartScreen       tui.Screen
	Bindings          *keybindings.Map
	SessionID         string
}

// RootModel owns the active screen, the terminal size, and the screen
// sub-models. It is the ONLY tea.Model: Init/Update/View live here, and each
// delegates to the active sub-model. Sub-models return their own concrete type
// from Update, so the root stores the result directly with no type assertion.
type RootModel struct {
	workspace                       tui.WorkspaceModel
	candidates                      []config.Candidate
	bootstrapStarted, discoveryBusy bool
	deps                            Deps
	screen                          tui.Screen
	status                          tui.StatusInfo // feeds the top bar
	clock                           time.Time      // live clock; updated by TickMsg
	lastCtrlC                       time.Time
	width                           int
	height                          int
	sessionID                       string
	healthOK                        bool // last model-health ping (drives the Status dashboard)
	healthMsg                       string
	lastCluster                     slurm.ClusterSnapshot // latest snapshot, to seed freshly built chats
	clusterGeneration               uint64
	splash                          tui.SplashModel
	server                          *ServerSession // non-nil after a successful splash server login
	wasBusy                         bool           // last observed chat busy state (turn-end push edge)
	chat                            tui.ChatModel
	continue_                       tui.ContinueModel
	settings                        tui.SettingsModel
	grid                            tui.GridModel
	statusScr                       tui.StatusModel
	modelsScr                       tui.ModelsModel
	cronScr                         tui.CronModel
	jobsScr                         tui.JobsModel
	plansScr                        tui.PlansModel
	env                             tui.EnvInfo
	sidebar                         tui.SidebarData
	// screenStack is the navigation history for esc-back. Chat (ScreenChat) is the
	// immutable root and is never pushed; when the stack is empty you're home and
	// esc is a no-op. Overlays are separate (overlay/overlayStack below).
	screenStack []tui.Screen
	// overlay is the topmost centered picker (effort/model chooser), or nil.
	// overlayStack holds suspended overlays so pickers can nest
	// (model chooser → effort → back).
	overlay           tui.Overlay
	overlayStack      []tui.Overlay
	bindings          *keybindings.Map
	statusLine        string // bottom-bar content
	sessionIn         int
	sessionOut        int
	lastContext       int
	sessionCycles     int
	sessionMsgs       int
	cachedStatusCmd   string    // last stdout from StatusLine.Command (async)
	lastStatusCmdPoll time.Time // wall clock of last status-command spawn
}

// NewRootModel builds a root from d, starting on d.StartScreen. The clock is seeded
// to now so the first paint isn't blank; a fresh ChatModel is built from the shared
// client + model id.
func NewRootModel(d Deps) RootModel {
	env := gatherEnv()
	sid := d.SessionID
	if sid == "" {
		sid = newSessionID()
	}
	m := RootModel{
		deps:      d,
		screen:    d.StartScreen,
		status:    d.Status,
		clock:     time.Now(),
		sessionID: sid,
		env:       env,
		sidebar:   tui.SidebarData{Env: env, Skills: skillNames(d.Skills)},
		splash:    tui.NewSplashModel(splashBoot(d.Live), sid),
		chat:      tui.NewChatModel(d.Live, d.Registry, d.Gate, sid, d.ChatSource).SetSkills(d.Skills).SetEnv(env),
		continue_: tui.NewContinueModel(d.ChatSource),
		settings:  tui.NewSettingsModel(d.Live, d.Settings),
		grid:      tui.NewGridModel(),
		statusScr: tui.NewStatusModel(statusInputs(d)),
		modelsScr: tui.NewModelsModel(d.Live),
		cronScr:   tui.NewCronModel(cronDataDir()),
		jobsScr:   tui.NewJobsModel(d.Scheduler),
		plansScr:  tui.NewPlansModel(d.Workflows),
		bindings:  d.Bindings,
	}
	m.settings = m.settings.SetEnv(env).SetSkills(skillPacks(d.Skills))
	m.chat = m.chat.SetParentContext(d.Context).SetSkillListing(d.SkillListing)
	if d.StartupNotice != "" {
		m.chat = m.chat.Notice(d.StartupNotice)
		m.splash = m.splash.WithNotice(d.StartupNotice)
	}
	m.refreshBindings()
	if d.ResumeCollective != "" {
		if resumed, err := tui.ResumeChatModel(d.Live, d.Registry, d.Gate, d.ChatSource, d.ResumeCollective); err == nil {
			m.chat = resumed.SetParentContext(d.Context).SetEnv(env).SetSkills(d.Skills).SetSkillListing(d.SkillListing)
		} else {
			m.chat = m.chat.Notice("Resume failed: " + err.Error())
		}
	}
	if d.Registry != nil {
		m.settings = m.settings.SetTools(d.Registry.Names())
	}
	return m
}

// statusInputs builds the Status page's external inputs: cached provider rows,
// per-model token usage, the registry's tool names, and the static environment.
func statusInputs(d Deps) tui.StatusInputs {
	tools := []string{}
	if d.Registry != nil {
		tools = d.Registry.Names()
	}
	var providers func() []tui.ProviderRow
	if d.Live != nil {
		live := d.Live
		providers = func() []tui.ProviderRow { return live.ProviderStatus() }
	}
	return tui.StatusInputs{
		Store:     d.Live,
		Status:    d.Status,
		HealthOK:  false,
		Health:    "checking…",
		Usage:     usageFunc(d.ChatSource),
		Providers: providers,
		Tools:     tools,
		Env:       gatherEnv(),
	}
}

// gatherEnv snapshots the static machine/session environment once at startup.
func gatherEnv() tui.EnvInfo {
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("LOGNAME")
	}
	tz, _ := time.Now().Zone()
	short := host
	if i := strings.IndexByte(host, '.'); i > 0 {
		short = host[:i]
	}
	long := host
	if !strings.Contains(host, ".") {
		// Hostname is a short label; /proc/sys/kernel/domainname completes it
		// when NIS/domain is configured (often "(none)" — leave as-is then).
		if dom := strings.TrimSpace(readSmallFile("/proc/sys/kernel/domainname")); dom != "" && dom != "(none)" {
			long = host + "." + dom
		}
	}
	return tui.EnvInfo{
		CVMFS:  dirExists("/cvmfs"),
		Module: os.Getenv("LMOD_CMD") != "" || os.Getenv("LMOD_DIR") != "",
		Slurm:  slurm.Detected(),
		Shell:  os.Getenv("SHELL"),
		Host:   host,
		User:   user,
		TZ:     tz,

		ShortName: short,
		LongName:  long,
		OSName:    osPrettyName(),
		Kernel:    strings.TrimSpace(readSmallFile("/proc/sys/kernel/osrelease")),
		Arch:      runtime.GOARCH,
		CPUs:      runtime.NumCPU(),
		MemGB:     memTotalGB(),
	}
}

// readSmallFile reads a small /proc-ish file ("" on any failure — a failed
// probe is a missing fact, never an error).
func readSmallFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// osPrettyName pulls PRETTY_NAME from /etc/os-release.
func osPrettyName() string { return osPrettyNameOf(readSmallFile("/etc/os-release")) }

// osPrettyNameOf is the pure parser (test seam).
func osPrettyNameOf(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

// memTotalGB reads MemTotal from /proc/meminfo (rounded down to GB).
func memTotalGB() int { return memTotalGBOf(readSmallFile("/proc/meminfo")) }

// memTotalGBOf is the pure parser (test seam).
func memTotalGBOf(content string) int {
	for _, line := range strings.Split(content, "\n") {
		if v, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			var kb int
			if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d kB", &kb); err == nil && kb > 0 {
				return kb / (1024 * 1024)
			}
			return 0
		}
	}
	return 0
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// advanceFromSplash is the shared splash→app transition: a working agentic
// model goes straight into a new chat (the navigation root — clear the
// stack); otherwise land in Settings at the add-provider area.
func (m RootModel) advanceFromSplash() (RootModel, tea.Cmd) {
	m.screenStack = nil
	m.chat = tui.NewChatModel(m.deps.Live, m.deps.Registry, m.deps.Gate, m.sessionID, m.deps.ChatSource).SetParentContext(m.deps.Context).SetEnv(m.env).
		SetCluster(m.lastCluster).
		SetSkills(m.deps.Skills).SetSkillListing(m.deps.SkillListing).
		Resize(m.width, m.height-tui.ChatChromeHeight(m.legendOn()))
	m.screen = tui.ScreenChat
	m.chatResize()
	return m, m.chat.Init()
}

// splashBoot resolves the chat role's model + provider for the splash status
// line, and reports whether the provider has a key (ready vs. not-ready hint).
func splashBoot(live *Settings) tui.BootInfo {
	if live == nil {
		return tui.BootInfo{Ready: false}
	}
	client, m, err := live.RoleClient(unimatrix.RoleChat)
	if err != nil || client == nil {
		return tui.BootInfo{Ready: false}
	}
	// m.Provider is already the config provider name; show it directly.
	return tui.BootInfo{Ready: true, Model: m.Label, Provider: m.Provider}
}

// Init starts the session-wide clock plus the entering screen's Init. Using
// activeInit (rather than splash.Init unconditionally) means --no-splash, which
// starts on the menu, doesn't spin up an off-screen splash timer.
func (m RootModel) Init() tea.Cmd {
	cmds := []tea.Cmd{tui.TickClock(), m.activeInit()}
	// Background Slurm poll: fetch immediately at login (only where Slurm
	// exists), then re-arm every 5 min from the handler. The Status page reads
	// the cached snapshot, so opening it never blocks.
	if cmd := m.statusLineCommand(); cmd != "" {
		cmds = append(cmds, pollStatusCmd(cmd))
	}
	return tea.Batch(cmds...)
}

func (m RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if input, ok := msg.(runnerInputMsg); ok {
		if input.input.ApprovalID != m.chat.ApprovalID() {
			return m, nil
		}
		if input.input.Key != nil {
			msg = *input.input.Key
		} else if input.input.Paste != nil {
			msg = tea.PasteMsg{Content: *input.input.Paste}
		}
	}
	next, cmd := m.update(msg)
	root := next.(RootModel)
	// Turn-end server sync: on the busy→idle edge, push the finished
	// collective to the logged-in server (async; failures arrive as
	// serverSyncResultMsg and surface as a chat notice, never a block).
	if root.server != nil {
		busy := root.chat.Busy()
		if root.wasBusy && !busy {
			if id := root.chat.CollectiveID(); id != "" {
				cmd = tea.Batch(cmd, pushChatCmd(root.deps.ChatSource, root.server, id))
			}
		}
		root.wasBusy = busy
	}
	if root.deps.Publish != nil {
		view := root.View()
		frame := RunnerFrame{View: view.Content, CollectiveID: root.chat.CollectiveID(), Busy: root.chat.Busy(), ApprovalID: root.chat.ApprovalID()}
		if view.Cursor != nil {
			frame.CursorVisible = true
			frame.CursorX = view.Cursor.X
			frame.CursorY = view.Cursor.Y
		}
		if err := root.deps.Publish(frame); err != nil {
			root.chat = root.chat.Stop().Notice("Resident persistence failed: " + err.Error())
			return root, nil
		}
	}
	return root, cmd
}

func (m RootModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch event := msg.(type) {
	case startupMsg:
		if m.deps.DisableMonitoring {
			return m, nil
		}
		m.discoveryBusy = true
		cmds := []tea.Cmd{m.discoverCmd(true), m.collectHostCmd()}
		if slurm.Detected() {
			cmds = append(cmds, pollClusterCmd())
		}
		return m, tea.Batch(cmds...)
	case candidatesMsg:
		m.discoveryBusy = false
		if event.Skip {
			return m, nil
		}
		m.candidates = event.Candidates
		if m.deps.Live != nil && (!event.Auto || m.screen == tui.ScreenSplash) {
			m.showCandidates()
		} else {
			m.chat = m.chat.Notice("Provider discovery complete. Ctrl+P → Discover AI providers to review.")
		}
		return m, nil
	case providerSavedMsg:
		if event.Err != nil {
			m.workspace.Notice = credential.Redact(event.Err.Error())
			return m, nil
		}
		m.modelsScr = tui.NewModelsModel(m.deps.Live).Resize(m.width, m.height)
		var cmd tea.Cmd
		m.modelsScr, cmd = m.modelsScr.Discover(event.Name)
		m.pushScreenOnce(tui.ScreenModels)
		return m, cmd
	case hostMsg:
		m.env.Observation = event.Record
		m.sidebar.Env = m.env
		m.settings = m.settings.SetEnv(m.env)
		m.statusScr = m.statusScr.SetEnv(m.env)
		m.chat = m.chat.SetEnv(m.env)
		if m.screen == tui.ScreenWorkspace && m.workspace.Title == "Hosts & Services" {
			m.showHosts()
		}
		return m, tea.Tick(30*time.Second, func(time.Time) tea.Msg { return hostTimerMsg{} })
	case hostTimerMsg:
		return m, m.collectHostCmd()
	case toolsReadyMsg:
		if event.Err != nil {
			m.workspace.Notice = "Scientific endpoint discovery failed; existing tools remain available."
			return m, nil
		}
		m.deps.Registry.Register(event.Tools...)
		m.settings = m.settings.SetTools(m.deps.Registry.Names())
		m.workspace.Notice = "Scientific tools refreshed"
		return m, nil
	case tui.WorkspaceAction:
		return m.workspaceAction(event)
	}
	if tui.IsPlansEvent(msg) {
		var cmd tea.Cmd
		m.plansScr, cmd = m.plansScr.Update(msg)
		return m, cmd
	}
	if tui.IsJobsEvent(msg) {
		var cmd tea.Cmd
		m.jobsScr, cmd = m.jobsScr.Update(msg)
		return m, cmd
	}
	if tui.IsChatEvent(msg) {
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		m.grid = m.grid.SetManifest(m.chat.Manifest())
		return m, cmd
	}
	if tui.IsModelsEvent(msg) {
		var cmd tea.Cmd
		m.modelsScr, cmd = m.modelsScr.Update(msg)
		return m, cmd
	}
	if tui.IsCronEvent(msg) {
		var cmd tea.Cmd
		m.cronScr, cmd = m.cronScr.Update(msg)
		return m, cmd
	}
	switch msg := msg.(type) {
	case tui.TickMsg:
		m.sidebar.Clock = time.Time(msg)
		if m.deps.Live != nil {
			m.sidebar.Providers = m.deps.Live.ProviderStatus() // cheap: cached breaker states
		}
		// Session-wide clock: store, re-arm (tea.Every fires once), and stamp the
		// Status page so its date/timezone ticks live. Handled before the screen
		// router so it ticks on every screen.
		m.clock = time.Time(msg)
		m.statusScr = m.statusScr.SetClock(m.clock)
		m.statusLine = m.renderStatusLine()
		cmds := []tea.Cmd{tui.TickClock()}
		// Refresh optional StatusLine.Command on an interval (never block the UI).
		// Init already kicked the first poll; re-arm only after we've seen a result
		// (lastStatusCmdPoll set) and the interval has elapsed.
		if cmd := m.statusLineCommand(); cmd != "" {
			if !m.lastStatusCmdPoll.IsZero() && time.Since(m.lastStatusCmdPoll) >= statusCmdInterval {
				m.lastStatusCmdPoll = time.Now()
				cmds = append(cmds, pollStatusCmd(cmd))
			}
		}
		return m, tea.Batch(cmds...)
	case statusCmdMsg:
		m.cachedStatusCmd = msg.out
		m.lastStatusCmdPoll = time.Now()
		m.statusLine = m.renderStatusLine()
		return m, nil
	case tui.SessionUsageMsg:
		m.sidebar.SessionIn, m.sidebar.SessionOut, m.sidebar.LastContext = msg.In, msg.Out, msg.LastContext
		if m.deps.Live != nil {
			if snap := m.deps.Live.Snapshot(); snap.Models != nil {
				if mm, ok := activeModelOf(snap); ok {
					m.sidebar.ContextWindow = mm.Context
				}
			}
		}
		m.sessionIn, m.sessionOut, m.lastContext = msg.In, msg.Out, msg.LastContext
		m.sessionCycles, m.sessionMsgs = msg.Cycles, msg.Messages
		m.statusScr = m.statusScr.SetSession(msg.In, msg.Out, msg.LastContext, msg.Cycles, msg.Messages)
		m.statusLine = m.renderStatusLine()
		return m, nil
	case modelHealthMsg:
		// Stored for the Status dashboard.
		m.healthOK, m.healthMsg = msg.ok, msg.reason
		m.statusScr = m.statusScr.SetHealth(msg.ok, msg.reason)
		return m, nil
	case clusterSnapshotMsg:
		// Background Slurm snapshot arrived: cache it on the Status page (its
		// cluster/fairshare/dirs sections) and the chat (for the model's cluster
		// blurb), then re-arm the next poll in 5 min.
		m.lastCluster = msg.snap
		m.sidebar.Cluster, m.sidebar.ClusterOK = msg.snap, !msg.snap.FetchedAt.IsZero()
		m.statusScr = m.statusScr.SetCluster(msg.snap)
		m.chat = m.chat.SetCluster(msg.snap)
		m.clusterGeneration++
		generation := m.clusterGeneration
		return m, tea.Tick(m.nextClusterPoll(), func(time.Time) tea.Msg { return clusterTimerMsg(generation) })
	case clusterTimerMsg:
		if uint64(msg) != m.clusterGeneration {
			return m, nil
		}
		return m, pollClusterCmd()
	case tui.RefreshClusterMsg:
		// `r` on the Status page: re-poll now.
		return m, pollClusterCmd()
	case tea.WindowSizeMsg:
		// Cache size and fan it out to every sub-model so each is sized before
		// it is first shown. Chat loses TopBarHeight rows to the top bar.
		m.width, m.height = msg.Width, msg.Height
		m.workspace = m.workspace.Resize(msg.Width, msg.Height)
		m.splash = m.splash.Resize(msg.Width, msg.Height)
		m.chatResize()
		m.continue_ = m.continue_.Resize(msg.Width, msg.Height)
		m.settings = m.settings.Resize(msg.Width, msg.Height)
		m.grid = m.grid.Resize(msg.Width, msg.Height)
		m.modelsScr = m.modelsScr.Resize(msg.Width, msg.Height)
		m.cronScr = m.cronScr.Resize(msg.Width, msg.Height)
		m.jobsScr = m.jobsScr.Resize(msg.Width, msg.Height)
		m.plansScr = m.plansScr.Resize(msg.Width, msg.Height)
		m.statusScr = m.statusScr.Resize(msg.Width, msg.Height).
			SetHealth(m.healthOK, m.healthMsg).
			SetSession(m.sessionIn, m.sessionOut, m.lastContext, m.sessionCycles, m.sessionMsgs)
		if m.overlay != nil {
			m.overlay = m.overlay.Resize(msg.Width, msg.Height)
		}
		if !m.bootstrapStarted {
			m.bootstrapStarted = true
			return m, tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return startupMsg{} })
		}
		return m, nil
	case tui.SplashAdvanceMsg:
		// Splash any-key: a working agentic model goes straight into a new chat
		// (the navigation root — clear the stack); otherwise land in Settings at
		// the add-provider area.
		return m.advanceFromSplash()
	case tui.SplashServerLoginMsg:
		// "Log in to server": async shared-password login; the result arrives
		// as ServerLoginResultMsg.
		return m, serverLoginCmd(m.deps.Live)
	case ServerLoginResultMsg:
		if msg.Err != "" {
			m.splash = m.splash.WithNotice("⚠ " + msg.Err)
			return m, nil
		}
		m.server = msg.Session
		m.splash = m.splash.WithNotice("")
		// Local settings edits push back to the roving document (fire-and-
		// forget; chat-sync errors surface via serverSyncResultMsg).
		if m.deps.Live != nil {
			sess := m.server
			m.deps.Live.SetOnSave(func(f config.File) { _ = sess.savePortable(f) })
		}
		next, cmd := m.advanceFromSplash()
		if s := msg.Session.SyncNotice; s != "" && next.screen == tui.ScreenChat {
			next.chat = next.chat.Notice(s)
		}
		return next, cmd
	case serverSyncResultMsg:
		if msg.Err != "" {
			m.chat = m.chat.Notice("⚠ " + msg.Err)
		}
		return m, nil
	case tui.BackMsg:
		// esc = back: pop the screen history. At the root (chat) it's a no-op.
		if n := len(m.screenStack); n > 0 {
			m.screen = m.screenStack[n-1]
			m.screenStack = m.screenStack[:n-1]
		}
		return m, nil
	case tui.ScreenChangeMsg:
		// A forward navigation (e.g. /settings, /context from chat): push the
		// current screen so esc can return to it.
		if msg.To == tui.ScreenContinue {
			m.continue_.Refresh()
		}
		m.pushScreenOnce(msg.To)
		return m, m.activeInit()
	case tui.ResumeChatMsg:
		if m.chat.Busy() {
			m.chat = m.chat.Notice("Interrupt the active turn before switching chats.")
			m.screen = tui.ScreenChat
			return m, nil
		}
		// Rebuild the chat model around the selected collective; resuming a chat
		// is a new root.
		cm, err := tui.ResumeChatModel(m.deps.Live, m.deps.Registry, m.deps.Gate, m.deps.ChatSource, msg.CollectID)
		if err != nil {
			m.continue_.SetError(err.Error())
			return m, nil
		}
		m.screenStack = nil
		m.chat = cm.SetParentContext(m.deps.Context).SetEnv(m.env).SetCluster(m.lastCluster).SetSkills(m.deps.Skills).SetSkillListing(m.deps.SkillListing)
		m.chatResize()
		m.screen = tui.ScreenChat
		return m, m.activeInit()
	case tui.ShowOverlayMsg:
		m.pushOverlay(msg.O)
		return m, nil
	case tui.OpenEffortMsg, tui.ShowEffortMsg:
		// ShowEffortMsg (from the model chooser's "e") nests under the current
		// overlay; OpenEffortMsg stands alone. pushOverlay handles both.
		cur := babel.EffortMedium
		if m.deps.Live != nil {
			cur = m.deps.Live.Effort()
		}
		m.pushOverlay(tui.NewEffortOverlay(cur))
		return m, nil
	case tui.OpenModelChooserMsg:
		if m.deps.Live == nil {
			return m, nil
		}
		m.pushOverlay(tui.NewModelChooser(m.deps.Live.AgenticModels(), m.deps.Live.AgenticModelID()))
		return m, nil
	case tui.EffortChosenMsg:
		if m.deps.Live != nil {
			if err := m.deps.Live.SetEffort(msg.E); err == nil {
				m.status.Effort = string(msg.E)
			}
		}
		return m, nil
	case tui.ModelChosenMsg:
		if m.deps.Live != nil {
			if label, err := m.deps.Live.SetAgenticModel(msg.ID); err == nil {
				m.status.Model = label
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+p" && !m.screenCapturesKeys() {
			m.showMenu()
			return m, nil
		}
		m.refreshBindings()
		// An open overlay owns all keys (it swallows F-keys too) until done.
		if m.overlay != nil {
			var cmd tea.Cmd
			m.overlay, cmd = m.overlay.Update(msg)
			if m.overlay != nil && m.overlay.Done() {
				m.popOverlay()
			}
			return m, cmd
		}
		if m.screen != tui.ScreenSplash && !m.screenCapturesKeys() {
			if action, ok := m.bindings.Resolve(keybindings.Global, msg.String()); ok {
				return m.handleAction(action)
			}
		}
		if msg.String() == "ctrl+c" {
			if m.screen == tui.ScreenChat && m.chat.Busy() {
				var cmd tea.Cmd
				m.chat, cmd = m.chat.Update(msg)
				return m, cmd
			}
			now := time.Now()
			if !m.lastCtrlC.IsZero() && now.Sub(m.lastCtrlC) <= 2*time.Second {
				return m, tea.Quit
			}
			m.lastCtrlC = now
			if m.screen == tui.ScreenChat {
				m.chat = m.chat.Notice("press ctrl+c again to quit")
			}
			return m, nil
		}
	}

	// Route everything else (keys, spinner ticks, hold timers) to the active
	// screen and capture its concrete return type.
	var cmd tea.Cmd
	switch m.screen {
	case tui.ScreenWorkspace:
		m.workspace, cmd = m.workspace.Update(msg)
	case tui.ScreenSplash:
		m.splash, cmd = m.splash.Update(msg)
	case tui.ScreenChat:
		m.chat, cmd = m.chat.Update(msg)
	case tui.ScreenContinue:
		m.continue_, cmd = m.continue_.Update(msg)
	case tui.ScreenSettings:
		m.settings, cmd = m.settings.Update(msg)
	case tui.ScreenGrid:
		m.grid, cmd = m.grid.Update(msg)
	case tui.ScreenStatus:
		m.statusScr, cmd = m.statusScr.Update(msg)
	case tui.ScreenModels:
		m.modelsScr, cmd = m.modelsScr.Update(msg)
	case tui.ScreenCron:
		m.cronScr, cmd = m.cronScr.Update(msg)
	case tui.ScreenJobs:
		m.jobsScr, cmd = m.jobsScr.Update(msg)
	case tui.ScreenPlans:
		m.plansScr, cmd = m.plansScr.Update(msg)
	}
	return m, cmd
}

func (m RootModel) handleAction(action keybindings.Action) (tea.Model, tea.Cmd) {
	switch action {
	case keybindings.Settings:
		// F2 — push Settings onto the nav stack (esc returns here).
		m.pushScreenOnce(tui.ScreenSettings)
		return m, m.settings.Init()
	case keybindings.Model:
		// F3 — the model chooser overlay (switch the running model).
		return m, func() tea.Msg { return tui.OpenModelChooserMsg{} }
	case keybindings.Sidebar:
		// F10 — cycle the live info column: auto → on → off (persisted), then
		// re-size the chat immediately so the column appears/disappears live.
		if m.deps.Live == nil {
			return m, nil
		}
		next := map[string]string{"auto": "on", "on": "off", "off": "auto"}[m.deps.Live.SidebarMode()]
		_ = m.deps.Live.SetSidebar(next)
		m.chatResize()
		return m, nil
	case keybindings.Cron:
		// F8 — manage + track the user's crontab.
		m.cronScr = m.cronScr.Resize(m.width, m.height)
		m.pushScreenOnce(tui.ScreenCron)
		return m, m.cronScr.Init()
	case keybindings.Models:
		// F11 — the Models screen (the catalog's own home).
		m.modelsScr = m.modelsScr.Resize(m.width, m.height)
		m.pushScreenOnce(tui.ScreenModels)
		return m, m.modelsScr.Init()
	case keybindings.Diagnostics:
		// F12 — the unified Status page (app/system); opening it forces an
		// instant cluster poll so the data is fresh.
		m.statusScr = m.statusScr.SetHealth(m.healthOK, m.healthMsg).SetClock(m.clock)
		m.pushScreenOnce(tui.ScreenStatus)
		return m, m.statusScr.Init()
	case keybindings.NewChat:
		if m.chat.Busy() {
			m.chat = m.chat.Notice("Interrupt the active turn before starting a new chat.")
			m.screen = tui.ScreenChat
			return m, nil
		}
		// F5 — a fresh chat is a new navigation root.
		m.screenStack = nil
		m.chat = tui.NewChatModel(m.deps.Live, m.deps.Registry, m.deps.Gate, m.sessionID, m.deps.ChatSource).SetParentContext(m.deps.Context).SetEnv(m.env).
			SetCluster(m.lastCluster).
			SetSkills(m.deps.Skills).SetSkillListing(m.deps.SkillListing)
		m.chatResize()
		m.screen = tui.ScreenChat
		return m, m.chat.Init()
	case keybindings.Resume:
		// F6 — push the Continue/rejoin picker.
		m.continue_.Refresh()
		m.pushScreenOnce(tui.ScreenContinue)
		return m, m.continue_.Init()
	case keybindings.Effort:
		// F4 — the effort picker overlay.
		return m, func() tea.Msg { return tui.OpenEffortMsg{} }
	case keybindings.Help:
		return m.workspaceAction(tui.WorkspaceAction{Kind: "help"})
	case keybindings.ContextView:
		// F7 — push the context grid.
		m.pushScreenOnce(tui.ScreenGrid)
		return m, m.grid.Init()
	case keybindings.QueenMode:
		if m.deps.Gate != nil {
			name := m.deps.Gate.CycleOpMode()
			m.status.Mode = name
			// Persist the chosen mode; report a failed save honestly rather
			// than letting the user believe the mode stuck.
			note := "permission mode → " + name
			if m.deps.Live != nil {
				f := m.deps.Live.Snapshot()
				if f.Permissions == nil {
					f.Permissions = &config.Permissions{}
				}
				f.Permissions.Mode = name
				f.PermissionMode = name
				if err := m.deps.Live.Save(f); err != nil {
					note += " (not saved: " + err.Error() + ")"
				}
			}
			m.chat = m.chat.Notice(note)
		} else {
			m.chat = m.chat.Notice("permission mode → " + m.status.Mode)
		}
	}
	return m, nil
}

// pushScreen records the current screen on the history stack and makes to the
// active one, so esc (BackMsg) can return to where you came from. Pointer
// receiver: mutates the root in place during Update/handleAction.
func (m *RootModel) pushScreen(to tui.Screen) {
	m.screenStack = append(m.screenStack, m.screen)
	m.screen = to
}

// pushScreenOnce pushes a screen only when it isn't already active, so
// pressing its F-key twice doesn't double-stack (which made the first esc pop
// back into the same screen).
func (m *RootModel) pushScreenOnce(to tui.Screen) {
	if m.screen != to {
		m.pushScreen(to)
	}
}

// screenCapturesKeys reports whether the active screen is consuming raw
// keystrokes (an inline text editor), in which case global keybindings must
// not fire — a user-rebound letter would be swallowed mid-typing.
func (m RootModel) screenCapturesKeys() bool {
	return (m.screen == tui.ScreenSettings && m.settings.CapturingKeys()) ||
		(m.screen == tui.ScreenModels && m.modelsScr.CapturingKeys()) ||
		(m.screen == tui.ScreenCron && m.cronScr.CapturingKeys())
}

// pushOverlay suspends the current overlay (if any) onto the stack and makes o
// the new topmost. Only a non-nil current overlay is pushed, so opening a
// top-level picker (when nothing is shown) leaves the stack empty and
// popOverlay clears the overlay instead of dereferencing a nil. Pointer
// receiver: mutates the root in place during Update.
func (m *RootModel) pushOverlay(o tui.Overlay) {
	if o == nil {
		return
	}
	if m.overlay != nil {
		m.overlayStack = append(m.overlayStack, m.overlay)
	}
	m.overlay = o.Resize(m.width, m.height)
}

// popOverlay drops the topmost overlay and restores the one beneath it (so a
// nested picker — effort under the model chooser — returns to its parent), or
// clears the overlay if there is no parent.
func (m *RootModel) popOverlay() {
	for n := len(m.overlayStack); n > 0; n = len(m.overlayStack) {
		top := m.overlayStack[n-1]
		m.overlayStack = m.overlayStack[:n-1]
		if top != nil {
			m.overlay = top.Resize(m.width, m.height)
			return
		}
	}
	m.overlay = nil
}

func checkModelCmd(live *Settings) tea.Cmd {
	return func() tea.Msg {
		if live == nil {
			return modelHealthMsg{reason: "no settings — F2 to configure"}
		}
		client, model, err := live.RoleClient(unimatrix.RoleAgentic)
		if err != nil {
			return modelHealthMsg{reason: err.Error() + " — F2 to configure"}
		}
		_, err = client.Chat(context.Background(), babel.ChatRequest{
			Model:          model.RequestID(),
			Messages:       []babel.Message{{Role: "user", Content: "Reply with OK."}},
			MaxTokens:      4,
			Effort:         babel.EffortOff,
			ReasoningStyle: model.EffectiveReasoningStyle(),
		})
		if err != nil {
			return modelHealthMsg{reason: err.Error() + " — F2 to configure"}
		}
		return modelHealthMsg{ok: true}
	}
}

// legendOn reports whether the F-key legend row is currently shown. Reads the
// live (cheap) setting so a change in Settings › Appearance takes effect on the
// next frame without a restart.
func (m RootModel) legendOn() bool { return m.deps.Live != nil && m.deps.Live.TopBarLegend() }

func (m RootModel) View() tea.View {
	var s string
	switch m.screen {
	case tui.ScreenWorkspace:
		s = m.workspace.View()
	case tui.ScreenSplash:
		s = m.splash.View()
	case tui.ScreenChat:
		bottom := tui.RenderBottomBar(m.width, m.statusLine)
		top := tui.RenderTopBar(m.width, m.clock, m.status)
		if m.legendOn() {
			top += "\n" + tui.RenderKeyLegendRow(m.width)
		}
		body := m.chat.View()
		if m.sidebarOn() {
			gutter := tui.RenderSidebarGutter(m.height - tui.ChatChromeHeight(m.legendOn()))
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, gutter, tui.RenderSidebar(m.sidebar, m.sidebarWidth(), m.height-tui.ChatChromeHeight(m.legendOn())))
		} else {
			top += "\n" + tui.CompactHost(m.env, m.width)
		}
		s = top + "\n" + body + "\n" + bottom
	case tui.ScreenContinue:
		s = m.continue_.View()
	case tui.ScreenSettings:
		s = m.settings.View()
	case tui.ScreenGrid:
		s = m.grid.View()
	case tui.ScreenStatus:
		s = m.statusScr.View()
	case tui.ScreenModels:
		s = m.modelsScr.View()
	case tui.ScreenCron:
		s = m.cronScr.View()
	case tui.ScreenJobs:
		s = m.jobsScr.View()
	case tui.ScreenPlans:
		s = m.plansScr.View()
	}
	// A centered overlay floats on top of whatever screen is active —
	// composited OVER it (the old version placed the card on an empty canvas,
	// hiding the screen underneath).
	if m.overlay != nil {
		s = tui.OverlayCenter(s, m.overlay.View())
	}
	v := tea.NewView(credential.Redact(s))
	v.AltScreen = true // declarative in v2 — no tea.WithAltScreen()
	// Place the input's real cursor when the chat is active and uncovered. (The
	// textinputs use a virtual cursor rendered in their own View, so this is a
	// no-op in practice — kept for parity with the reference.)
	if m.screen == tui.ScreenChat && m.overlay == nil {
		v.Cursor = m.chat.Cursor()
	}
	return v
}

// activeInit returns the entering screen's Init Cmd after a transition.
func (m RootModel) activeInit() tea.Cmd {
	switch m.screen {
	case tui.ScreenSplash:
		return m.splash.Init()
	case tui.ScreenChat:
		return m.chat.Init()
	case tui.ScreenContinue:
		return m.continue_.Init()
	case tui.ScreenSettings:
		return m.settings.Init()
	case tui.ScreenGrid:
		return m.grid.Init()
	case tui.ScreenStatus:
		return m.statusScr.Init()
	case tui.ScreenModels:
		return m.modelsScr.Init()
	case tui.ScreenCron:
		return m.cronScr.Init()
	case tui.ScreenJobs:
		return m.jobsScr.Init()
	case tui.ScreenPlans:
		return m.plansScr.Init()
	}
	return nil
}

// usageFunc builds the Status dashboard's per-model token aggregator. It pulls a
// store from the chat source and, if the store reports usage (SQLiteStore does),
// returns its aggregated totals; otherwise nil (the dashboard shows "—").
func usageFunc(src history.ChatStoreSource) func() map[string]history.Cost {
	if src == nil {
		return nil
	}
	return func() map[string]history.Cost {
		if ur, ok := src().(history.UsageReporter); ok {
			if m, err := ur.UsageByModel(); err == nil {
				return m
			}
		}
		return nil
	}
}

// Cluster poll cadence: fast while a live view (the sidebar or the Status
// page) is showing, slow in the background — the data is LIVE when watched.
const (
	clusterPollFast = 3 * time.Minute
	clusterPollSlow = 3 * time.Minute
)

func (m RootModel) nextClusterPoll() time.Duration {
	if m.sidebarOn() || m.screen == tui.ScreenStatus {
		return clusterPollFast
	}
	return clusterPollSlow
}

// clusterSnapshotMsg carries a background Slurm snapshot to the Status page.
type clusterSnapshotMsg struct{ snap slurm.ClusterSnapshot }
type clusterTimerMsg uint64

// pollCluster fetches one Slurm snapshot (blocking; run in a Cmd goroutine).
func pollCluster() clusterSnapshotMsg {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return clusterSnapshotMsg{snap: slurm.Snapshot(ctx)}
}

// pollClusterCmd wraps pollCluster as an immediate tea.Cmd.
func pollClusterCmd() tea.Cmd {
	return func() tea.Msg { return pollCluster() }
}

// statusLineCommand returns the configured StatusLine.Command, or "".
func (m RootModel) statusLineCommand() string {
	if m.deps.Live == nil {
		return ""
	}
	sl := m.deps.Live.Snapshot().StatusLine
	if sl == nil || !sl.Enabled || sl.Command == "" {
		return ""
	}
	return sl.Command
}

// pollStatusCmd runs cmd via the user's shell and returns its first-line stdout
// trimmed. Failures yield an empty string (bar simply omits the segment).
func pollStatusCmd(cmdline string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		out, err := exec.CommandContext(ctx, shell, "-c", cmdline).Output()
		if err != nil {
			return statusCmdMsg{}
		}
		line := strings.TrimSpace(string(out))
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		// Cap length so a runaway command can't blow out the bottom bar.
		if len(line) > 80 {
			line = line[:77] + "…"
		}
		return statusCmdMsg{out: line}
	}
}

// renderStatusLine builds the bottom-bar content from config StatusLine + live
// session state. Runs off the 1 Hz tick; never blocks on a shell command here
// (command output is refreshed asynchronously via pollStatusCmd).
func (m RootModel) renderStatusLine() string {
	cfg := config.File{}
	if m.deps.Live != nil {
		cfg = m.deps.Live.Snapshot()
	}
	sl := cfg.StatusLine
	// When unset, treat as enabled with default segments so the grey bottom
	// band always shows something (cwd · model · mode) instead of looking blank.
	if sl != nil && !sl.Enabled {
		return ""
	}
	segs := []string{"cwd", "model", "mode"}
	var cmdOut string
	if sl != nil {
		if len(sl.Segments) > 0 {
			segs = sl.Segments
		} else {
			segs = []string{"cwd", "model", "mode", "tokens"}
		}
		if sl.Command != "" {
			cmdOut = m.cachedStatusCmd
		}
	}
	var parts []string
	for _, seg := range segs {
		switch strings.ToLower(seg) {
		case "cwd":
			if wd, err := os.Getwd(); err == nil {
				parts = append(parts, shortHome(wd))
			}
		case "model":
			if m.status.Model != "" {
				parts = append(parts, m.status.Model)
			}
		case "mode":
			if m.status.Mode != "" {
				parts = append(parts, m.status.Mode)
			}
		case "tokens":
			if m.sessionIn+m.sessionOut > 0 {
				parts = append(parts, fmt.Sprintf("↑%s ↓%s",
					tuiFormatTokens(m.sessionIn), tuiFormatTokens(m.sessionOut)))
			}
		case "clock":
			parts = append(parts, m.clock.Format("15:04:05"))
		case "git":
			if b := gitBranch(); b != "" {
				parts = append(parts, b)
			}
		}
	}
	if cmdOut != "" {
		parts = append(parts, cmdOut)
	}
	return strings.Join(parts, " · ")
}

func tuiFormatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%dk", n/1000)
}

func shortHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func gitBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// newSessionID generates a short random session identifier.
func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("sess_%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("sess_%s", hex.EncodeToString(b))
}

// skillNames pulls the pack names out of the compact skills listing (one
// "name: description" per line).
func skillNames(listing string) []string {
	var out []string
	for _, sk := range skillPacks(listing) {
		out = append(out, sk.Name)
	}
	return out
}

// skillPacks parses the listing into name+description pairs.
func skillPacks(listing string) []tui.SkillPack {
	var out []tui.SkillPack
	for _, line := range strings.Split(listing, "\n") {
		name, desc, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, tui.SkillPack{Name: strings.TrimSpace(name), Desc: strings.TrimSpace(desc)})
	}
	return out
}

// activeModelOf resolves the running (agentic, falling back to chat) model
// from a config snapshot — the sidebar's context-window source.
func activeModelOf(f config.File) (unimatrix.Model, bool) {
	role := f.Roles[unimatrix.RoleAgentic]
	if role == "" {
		role = f.Roles[unimatrix.RoleChat]
	}
	for _, mm := range f.Models {
		if mm.ID == role {
			return mm, true
		}
	}
	return unimatrix.Model{}, false
}

// chatResize sizes the chat to the terminal, minus the live info column and
// its gutter when the sidebar is showing — the ONE place the narrowed width
// is computed (splash advance, resume, F5 new chat, F10 toggle, and
// WindowSizeMsg all funnel through here; previously only WindowSizeMsg did,
// so every rebuilt chat was full-width and the joined sidebar overflowed the
// terminal, clipping the column away entirely).
func (m *RootModel) chatResize() {
	chatW := m.width
	if m.sidebarOn() {
		chatW = m.width - m.sidebarWidth() - 1
	}
	height := m.height - tui.ChatChromeHeight(m.legendOn())
	if !m.sidebarOn() {
		height--
	}
	m.chat = m.chat.Resize(chatW, height)
}

func (m RootModel) sidebarWidth() int {
	w := tui.SidebarWidth
	if m.deps.Live != nil {
		f := m.deps.Live.Snapshot()
		if f.Appearance != nil && f.Appearance.SidebarWidth >= 32 && f.Appearance.SidebarWidth <= 60 {
			w = f.Appearance.SidebarWidth
		}
	}
	return w
}

// sidebarOn reports whether the chat screen's live info column is showing:
// auto = only on very wide terminals (>=160 cols); on = forced (>=120); off =
// never.
func (m RootModel) sidebarOn() bool {
	mode := "auto"
	if m.deps.Live != nil {
		mode = m.deps.Live.SidebarMode()
	}
	switch mode {
	case "off":
		return false
	case "on":
		return m.width >= m.sidebarWidth()+61
	default: // auto
		return m.width >= m.sidebarWidth()+61
	}
}

// cronDataDir resolves the cron store's data directory (DataDir on success,
// the legacy default otherwise).
func cronDataDir() string {
	if dir, err := config.DataDir(); err == nil {
		return dir
	}
	return os.Getenv("HOME") + "/.deepthought"
}
