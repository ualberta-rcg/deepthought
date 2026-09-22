// Command deepthought-cli is the DeepThought entrypoint. By default it runs the TUI on the
// caller's terminal; pass --sub-etha <addr> (e.g. :2323) to serve the same TUI
// over SSH via Wish. Both paths drive the same RootModel.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"

	"charm.land/bubbletea/v2"
	"charm.land/wish/v2"
	"charm.land/wish/v2/activeterm"
	btmw "charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/logging"

	"deepthought-cli/internal/app"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/historytools"
	"deepthought-cli/internal/keybindings"
	"deepthought-cli/internal/queen"
	"deepthought-cli/internal/science"
	"deepthought-cli/internal/skills"
	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tools"
	"deepthought-cli/internal/transwarp"
	"deepthought-cli/internal/tui"
	"deepthought-cli/internal/unimatrix"
	"deepthought-cli/internal/workflow"
)

func init() {
	history.RegisterExtensionType("read", func() any { return &history.ReadDocExt{} })
	history.RegisterExtensionType("bash", func() any { return &history.BashExt{} })
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(versionString())
		return
	}
	attachID, handled := handleResidentVerb(os.Args[1:])
	if handled {
		return
	}
	if attachID != "" {
		os.Args = []string{os.Args[0], "--no-splash"}
	}
	subEtha := flag.String("sub-etha", "", "SSH listen address (e.g. :2323). Empty = run on the local TTY.")
	configPath := flag.String("config", "", "path to settings file (default: ~/.deepthought/config.json; $DEEPTHOUGHT_CLI_HOME overrides the dir)")
	noSplash := flag.Bool("no-splash", false, "skip the splash screen and start straight in chat")
	towel := flag.Bool("towel", false, "start or attach the user-space resident daemon")
	flag.Parse()
	if *towel {
		if err := startResident(*configPath); err != nil {
			fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
			os.Exit(1)
		}
		response, err := sendControl(transwarp.Request{Operation: transwarp.NewSession})
		if err == nil {
			err = runAttached(response.Message)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
		}
		return
	}

	cfg, cfgPath, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
		os.Exit(1)
	}

	if cfg.Local != nil {
		defer cfg.Local.Close()
	}
	deps, droneStore := buildRuntime(cfg, cfgPath, attachID)
	if droneStore == nil {
		return
	}
	defer droneStore.Close()
	if *noSplash {
		deps.StartScreen = tui.ScreenChat
	}

	if *subEtha == "" {
		deps.Status.Addr = "local"
		deps.Settings.Addr = "local"
		runLocal(deps)
		return
	}
	if err := runSSH(*subEtha, deps); err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
		os.Exit(1)
	}
}

var buildVersion string

func versionString() string {
	if buildVersion != "" {
		return "DeepThought " + buildVersion
	}
	version := "development"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				version = setting.Value
			}
		}
	}
	return "DeepThought " + version
}

// loadConfig resolves the settings path (--config flag → ~/.deepthought/config.json)
// and loads it, returning the resolved path for the settings screen. A missing
// file yields built-in defaults; a present-but-invalid file is fatal. A missing
// API key is non-fatal but warned: the chat loop needs it.
func loadConfig(path string) (*config.Config, string, error) {
	explicit := path != ""
	if path == "" {
		var err error
		path, err = config.DefaultPath()
		if err != nil {
			return nil, "", fmt.Errorf("resolve config path: %w", err)
		}
	}
	cfg, err := config.OpenLocal(path, explicit)
	if err != nil {
		return nil, "", fmt.Errorf("load config: %w", err)
	}
	return cfg, path, nil
}

// buildGate constructs a per-process Queen gate from config permissions, with
// an OnPersist callback that writes always-allow/deny rules back to config.
func buildGate(cfg *config.Config, live *app.Settings) *queen.Gate {
	mode := permissionModeLabel(cfg)
	rules := queen.Rules{}
	if cfg.Permissions != nil {
		rules.Allow = append([]string(nil), cfg.Permissions.Allow...)
		rules.Ask = append([]string(nil), cfg.Permissions.Ask...)
		rules.Deny = append([]string(nil), cfg.Permissions.Deny...)
	}
	gate := queen.NewGateFromConfig(mode, rules, nil)
	gate.OnPersist = func(decision queen.DecisionName, rule string) {
		if live == nil || rule == "" {
			return
		}
		f := live.Snapshot()
		if f.Permissions == nil {
			f.Permissions = &config.Permissions{Mode: mode}
		}
		switch decision {
		case queen.DecAllow:
			f.Permissions.Allow = appendUnique(f.Permissions.Allow, rule)
		case queen.DecDeny:
			f.Permissions.Deny = appendUnique(f.Permissions.Deny, rule)
		case queen.DecAsk:
			f.Permissions.Ask = appendUnique(f.Permissions.Ask, rule)
		}
		_ = live.Save(f)
	}
	return gate
}

func permissionModeLabel(cfg *config.Config) string {
	if cfg.Permissions != nil && cfg.Permissions.Mode != "" {
		return queen.LookupMode(cfg.Permissions.Mode, nil).Name
	}
	switch cfg.PermissionMode {
	case "always-proceed", "auto":
		return "auto"
	case "safe-auto":
		return "safe-auto"
	default:
		return "safe"
	}
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// runLocal drives the root model on the caller's TTY. Alt-screen and the cursor
// are declarative on the View (set in RootModel.View), so no program options are
// needed for them. Color profile is forced to ANSI 256 so PuTTY/SSH clients
// that advertise plain TERM=xterm still get a visible chrome band.
func runLocal(deps app.Deps) {
	ctx, cancel := context.WithCancel(context.Background())
	defer deps.Registry.Close()
	defer cancel()
	deps.Context = ctx
	m := app.NewRootModel(deps)
	opts := app.ProgramColorOpts(os.Environ())
	p := tea.NewProgram(m, opts...)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
		os.Exit(1)
	}
}

// runSSH serves the root model over SSH. The host key lives under $SCRATCH
// (never committed); Wish auto-generates an ed25519 key on first run.
func runSSH(addr string, deps app.Deps) error {
	hostKey := filepath.Join(os.Getenv("SCRATCH"), "deepthought-cli", "host_ed25519")

	srv, err := wish.NewServer(
		wish.WithAddress(addr),
		wish.WithHostKeyPath(hostKey),
		wish.WithMiddleware(
			btmw.Middleware(app.SSHHandler(addr, deps)),
			activeterm.Middleware(), // reject non-interactive sessions
			logging.Middleware(),    // connection log
		),
	)
	if err != nil {
		return fmt.Errorf("wish server: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	return srv.ListenAndServe()
}

// buildRuntime is shared by standalone and resident sessions.
func buildRuntime(cfg *config.Config, cfgPath, attachID string) (app.Deps, *history.SQLiteStore) {
	// Build the live settings handle (role resolution + pooled clients), the
	// shared tool registry, and the Queen gate once. The pointers are safe for
	// concurrent use, so one instance set serves every session.
	live := app.NewSettings(cfg, cfgPath)
	dataDir, err := config.DataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
		os.Exit(1)
	}
	droneStore, err := history.NewSQLiteStore(filepath.Join(dataDir, "history.db"), filepath.Join(dataDir, "bodies"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli: open history:", err)
		os.Exit(1)
	}
	workDir, _ := os.Getwd()
	fileTools, err := tools.NewWorkspaceTools(workDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli: workspace:", err)
		return app.Deps{}, nil
	}
	nativeTools := []tools.Tool{tools.NewGuardedBash(), historytools.NewExpand(droneStore)}
	nativeTools = append(nativeTools, fileTools...)
	scheduler := slurm.NewClient(nil)
	scheduler.Store = droneStore
	nativeTools = append(nativeTools, (slurm.ToolSet{Client: scheduler}).Tools()...)
	workflows := &workflow.Service{Store: droneStore, Scheduler: scheduler, Version: buildVersion}
	nativeTools = append(nativeTools, &workflow.Tool{Service: workflows})

	// Load skills from every install location (user, project codex/claude dirs,
	// and org-managed system roots) and expose them to the model: a compact index
	// in the system prompt plus a read-only `skill` tool to load bodies on demand
	// (progressive disclosure). Best-effort — a load failure just means no skills
	// surface.
	catalog, skillErr := skills.NewCatalog(workDir)
	if skillErr != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli: some skill packs could not be loaded:", skillErr)
	}
	nativeTools = append(nativeTools, &tools.Skill{LiveNames: catalog.Names, LiveLookup: catalog.Lookup})
	registry := tools.NewRegistry(nativeTools...)
	gate := buildGate(cfg, live)

	chatDir, err := config.ChatDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
		os.Exit(1)
	}
	if err := droneStore.MigrateLegacyChats(chatDir); err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli: migrate legacy chats:", err)
		os.Exit(1)
	}

	// The top bar mirrors the chat role's model at boot (settings edits
	// update it live).
	statusModel := "?"
	if _, m, err := live.RoleClient(unimatrix.RoleChat); err == nil {
		statusModel = m.Label
	}
	permMode := permissionModeLabel(cfg)
	deps := app.Deps{
		StartupNotice: cfg.StartupNotice,
		DiscoverTools: func(ctx context.Context) ([]tools.Tool, error) {
			return science.Configured(ctx, live.Snapshot().Providers, nativeTools)
		},
		Status: tui.StatusInfo{
			Model:  statusModel,
			Mode:   permMode,
			Effort: cfg.EffortLevel(),
		},
		Settings: tui.SettingsInfo{
			ConfigPath: cfgPath,
			Mode:       permMode,
		},
		Live:         live,
		Scheduler:    scheduler,
		Workflows:    workflows,
		Registry:     registry,
		Gate:         gate,
		Skills:       catalog.Listing(),
		SkillListing: catalog.Listing, ReloadSkills: catalog.Reload,
		// The chat persists through SQLite (history.db) — the authoritative
		// store. The source returns the shared, stateless SQLiteStore; each new
		// chat CreateCollective-s a fresh collective routed by its own ID.
		ChatSource:  func() history.ChatStore { return droneStore },
		StartScreen: tui.ScreenSplash,
		Bindings:    keybindings.Defaults(),
		SessionID:   attachID,
	}
	return deps, droneStore
}
