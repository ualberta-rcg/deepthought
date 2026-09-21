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
)

var (
	addrFlag     = flag.String("addr", "127.0.0.1:8080", "listen address (the container overrides to 0.0.0.0:8080)")
	dataFlag     = flag.String("data", "", "data directory (default: the DeepThought data dir)")
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
	password := loadPassword()

	api := &dserver.API{
		Start:    time.Now(),
		DataDir:  dataDir,
		Password: password,
		Sessions: dserver.NewSessionStore(24 * time.Hour),
		Version:  version(),
	}
	if dsn := os.Getenv("DEEPTHOUGHT_MYSQL_DSN"); dsn != "" {
		db, users, err := dserver.OpenDB(dsn)
		if err != nil {
			log.Fatalf("%v", err)
		}
		defer db.Close()
		api.DB, api.Users = db, users
		log.Print("database: connected (schema ensured)")
	} else {
		log.Print("database: not configured (user settings + chats endpoints 503 — set DEEPTHOUGHT_MYSQL_DSN)")
	}
	httpSrv := &http.Server{
		Addr:              *addrFlag,
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
			*addrFlag, dataDir, authState(password))
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

func loadPassword() string {
	if *passwordFlag != "" {
		b, err := os.ReadFile(*passwordFlag)
		if err != nil {
			log.Fatalf("password file: %v", err)
		}
		return strings.TrimSpace(string(b))
	}
	return os.Getenv("DEEPTHOUGHT_SERVER_PASSWORD")
}

func authState(password string) string {
	if password == "" {
		return "OFF (all /api endpoints 503 — set DEEPTHOUGHT_SERVER_PASSWORD or --password-file)"
	}
	return "shared password + session tokens"
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func version() string {
	if v := os.Getenv("DEEPTHOUGHT_VERSION"); v != "" {
		return v
	}
	return "dev"
}
