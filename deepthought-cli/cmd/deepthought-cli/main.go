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
	"deepthought-cli/internal/skills"
	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tools"
	"deepthought-cli/internal/tui"
	"deepthought-cli/internal/unimatrix"
)

func init() {
	history.RegisterExtensionType("read", func() any { return &history.ReadDocExt{} })
	history.RegisterExtensionType("bash", func() any { return &history.BashExt{} })
}

func main() {
	attachID, handled := handleResidentVerb(os.Args[1:])
	if handled {
		return
	}
	if attachID != "" {
		os.Args = []string{os.Args[0], "--no-splash"}
	}
	subEtha := flag.String("sub-etha", "", "SSH listen address (e.g. :2323). Empty = run on the local TTY.")
	configPath := flag.String("config", "", "path to settings file (default: ~/.deepthought/config.json; $DEEPTHOUGHT_CLI_HOME overrides the dir)")
	noSplash := flag.Bool("no-splash", false, "skip the splash screen and start at the menu")
	towel := flag.Bool("towel", false, "start or attach the user-space resident daemon")
	flag.Parse()
	if *towel {
		if err := startResident(); err != nil {
			fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
			os.Exit(1)
		}
	}

	cfg, cfgPath, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
		os.Exit(1)
	}

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
	defer droneStore.Close()
	nativeTools := []tools.Tool{tools.NewGuardedBash(), tools.NewRead(), historytools.NewExpand(droneStore)}
	nativeTools = append(nativeTools, (slurm.ToolSet{Client: slurm.NewClient(nil)}).Tools()...)

	// Load skills from every install location (user, project codex/claude dirs,
	// and org-managed system roots) and expose them to the model: a compact index
	// in the system prompt plus a read-only `skill` tool to load bodies on demand
	// (progressive disclosure). Best-effort — a load failure just means no skills
	// surface.
	workDir, _ := os.Getwd()
	skillList, _ := skills.NewLoader().Load(workDir)
	if len(skillList) > 0 {
		skillByName := make(map[string]*skills.Skill, len(skillList))
		skillNames := make([]string, 0, len(skillList))
		for _, s := range skillList {
			if _, ok := skillByName[s.Name]; !ok {
				skillNames = append(skillNames, s.Name)
			}
			skillByName[s.Name] = s
		}
		nativeTools = append(nativeTools, tools.NewSkillTool(skillNames, func(name string) (string, bool, error) {
			s, ok := skillByName[name]
			if !ok {
				return "", false, nil
			}
			body, err := s.Body()
			return body, true, err
		}))
	}
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
		Status: tui.StatusInfo{
			Model:  statusModel,
			Mode:   permMode,
			Effort: cfg.EffortLevel(),
		},
		Settings: tui.SettingsInfo{
			ConfigPath: cfgPath,
			Mode:       permMode,
		},
		Live:     live,
		Registry: registry,
		Gate:     gate,
		Skills:   skills.Listing(skillList),
		// The chat persists through SQLite (history.db) — the authoritative
		// store. The source returns the shared, stateless SQLiteStore; each new
		// chat CreateCollective-s a fresh collective routed by its own ID.
		ChatSource:  func() history.ChatStore { return droneStore },
		StartScreen: tui.ScreenSplash,
		Bindings:    keybindings.Defaults(),
		SessionID:   attachID,
	}
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

// loadConfig resolves the settings path (--config flag → ~/.deepthought/config.json)
// and loads it, returning the resolved path for the settings screen. A missing
// file yields built-in defaults; a present-but-invalid file is fatal. A missing
// API key is non-fatal but warned: the chat loop needs it.
func loadConfig(path string) (*config.Config, string, error) {
	if path == "" {
		var err error
		path, err = config.DefaultPath()
		if err != nil {
			return nil, "", fmt.Errorf("resolve config path: %w", err)
		}
	}
	if err := config.LoadSecrets(path); err != nil {
		return nil, "", fmt.Errorf("load provider secrets: %w", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, "", fmt.Errorf("load config: %w", err)
	}
	for _, p := range cfg.Providers {
		if p.ExpandedKey() == "" {
			fmt.Fprintf(os.Stderr, "deepthought-cli: warning: provider %q has an empty api_key — its models will fail until it's set\n", p.Name)
		}
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
