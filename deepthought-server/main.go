// Command deepthought-server is the server-side skeleton: an HTTP API for
// the DeepThought research-copilot roadmap. It deploys as a container on the
// Vulcan Kubernetes cluster (deepthought.vulcan.alliancecan.ca terminates TLS
// upstream; this server binds plaintext) and starts life as health endpoints
// plus documented 501 placeholders — the execution plane grows in behind the
// same front door. See deepthought-cli/docs/SERVER.md.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	dserver "deepthought-server/server"
	"deepthought-server/store"
)

var (
	addrFlag     = flag.String("addr", "127.0.0.1:8080", "listen address (the container overrides to 0.0.0.0:8080)")
	dataFlag     = flag.String("data", "", "data directory (default: the DeepThought data dir)")
	configFlag   = flag.String("config", "", "server JSON config file (default: $DEEPTHOUGHT_SERVER_CONFIG or <data>/server.json)")
	passwordFlag = flag.String("password-file", "", "file containing the shared login password (overrides $DEEPTHOUGHT_SERVER_PASSWORD)")
)

func main() {
	flag.Parse()

	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = os.Getenv("DEEPTHOUGHT_DATA_DIR")
	}
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("data dir: %v", err)
		}
		dataDir = filepath.Join(home, ".deepthought-server")
	}
	if err := dserver.EnsureDirs(dataDir); err != nil {
		log.Fatalf("data dirs: %v", err)
	}
	cfg, err := dserver.LoadServerConfig(*configFlag, dataDir)
	if err != nil {
		log.Fatalf("server config: %v", err)
	}
	if cfg.DataDir != "" {
		dataDir = cfg.DataDir
	}
	_ = dserver.WriteExampleServerConfig(dataDir)

	password := loadPassword(cfg)
	ttl := time.Duration(cfg.SessionTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	api := &dserver.API{
		Start:    time.Now(),
		DataDir:  dataDir,
		Password: password,
		Sessions: dserver.NewSessionStore(ttl),
		Version:  version(),
		Admins:   adminUsers(cfg.AdminUsers, os.Getenv("DEEPTHOUGHT_ADMIN_USERS")),
		Config:   cfg,
		OIDC:     dserver.NewOIDCProvider(cfg.Auth.OIDC),
	}
	dsn := cfg.MySQLDSN
	if dsn != "" {
		db, users, err := dserver.OpenDB(dsn)
		if err != nil {
			log.Fatalf("%v", err)
		}
		defer db.Close()
		api.DB, api.Users = db, users
		api.Credentials = store.NewCredentialDB(db)
		api.Sessions.UseBackend(store.NewSessionDB(db))
		go purgeSessions(context.Background(), api.Sessions)
		log.Print("database: connected (schema ensured; sessions persisted)")
	} else {
		log.Print("database: not configured (user settings + chats endpoints 503 — set DEEPTHOUGHT_MYSQL_DSN)")
	}
	if key := cfg.VaultKey; key != "" {
		vault, err := dserver.NewVault(key)
		if err != nil {
			log.Fatalf("DEEPTHOUGHT_VAULT_KEY: %v", err)
		}
		api.Vault = vault
		log.Print("credential vault: configured")
	} else {
		log.Print("credential vault: not configured (only $ENV credential references can be synced — set DEEPTHOUGHT_VAULT_KEY)")
	}
	listenAddr := *addrFlag
	if listenAddr == "127.0.0.1:8080" && cfg.ListenAddr != "" {
		listenAddr = cfg.ListenAddr
	}
	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           logRequests(dserver.NewMux(api)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown: SIGINT/SIGTERM drain in-flight requests (the resident
	// daemon lacks this; the server does not).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("deepthought-server listening on %s (data: %s, auth: %s)",
			listenAddr, dataDir, authState(cfg, password))
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	case <-ctx.Done():
		log.Print("shutting down…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		log.Print("bye")
	}
}

// SIGHUP notes a reload request (config + skills are re-read on demand; the
// admin/reload endpoint is the programmatic form). Kept alive for ops habit.
func init() {
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGHUP)
		for range sigs {
			log.Print("SIGHUP: reload (config + skills are re-read on next use)")
		}
	}()
}

func loadPassword(cfg dserver.ServerConfig) string {
	pwFile := *passwordFlag
	if pwFile == "" {
		pwFile = cfg.Auth.PasswordFile
	}
	if pwFile != "" {
		b, err := os.ReadFile(pwFile)
		if err != nil {
			log.Fatalf("password file: %v", err)
		}
		return strings.TrimSpace(string(b))
	}
	if cfg.Auth.Password != "" {
		return cfg.Auth.Password
	}
	return os.Getenv("DEEPTHOUGHT_SERVER_PASSWORD")
}

func authState(cfg dserver.ServerConfig, password string) string {
	pwOn := cfg.Auth.PasswordEnabled(password)
	oidcOn := cfg.Auth.OIDCEnabled()
	switch {
	case pwOn && oidcOn:
		return fmt.Sprintf("hybrid (shared password + OIDC %s)", cfg.Auth.OIDC.Issuer)
	case oidcOn:
		return fmt.Sprintf("OIDC (%s)", cfg.Auth.OIDC.Issuer)
	case pwOn:
		return "shared password + session tokens (OIDC configured, inactive)"
	default:
		return "OFF (all /api endpoints 503 — set DEEPTHOUGHT_SERVER_PASSWORD or enable OIDC in server.json)"
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

// buildVersion is stamped at image build time (-X main.buildVersion=…).
var buildVersion = ""

func version() string {
	if v := os.Getenv("DEEPTHOUGHT_VERSION"); v != "" {
		return v
	}
	if buildVersion != "" {
		return buildVersion
	}
	return "dev"
}

// adminUsers parses configured and comma-separated login-name lists.
func adminUsers(cfgAdmins []string, list string) map[string]bool {
	out := map[string]bool{}
	for _, name := range cfgAdmins {
		if name = strings.TrimSpace(name); name != "" {
			out[name] = true
		}
	}
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out[name] = true
		}
	}
	return out
}

// purgeSessions deletes expired sessions hourly.
func purgeSessions(ctx context.Context, sessions *dserver.SessionStore) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := sessions.Purge(); err != nil {
			log.Printf("session purge: %v", err)
		} else if n > 0 {
			log.Printf("session purge: %d expired", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
