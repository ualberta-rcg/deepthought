// HTTP surface of deepthought-server: the embedded web UI, health/version
// endpoints (open, for probes), a login endpoint issuing session tokens, and
// session-protected /api/v1 routes. See docs/SERVER.md.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/skills"
)

//go:embed web
var webFiles embed.FS

// API is the server's state (wired by cmd/deepthought-server).
type API struct {
	Start   time.Time
	DataDir string
	// Password is the shared phase-1 secret ($DEEPTHOUGHT_SERVER_PASSWORD).
	// Empty means auth is not configured: every protected route answers 503,
	// never silently open.
	Password string
	// Sessions issues and validates login tokens.
	Sessions *SessionStore
	Version  string
}

// NewMux builds the full route table.
func NewMux(a *API) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.health)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("GET /api/v1/version", a.auth(a.version))
	mux.HandleFunc("GET /api/v1/crons", a.auth(a.crons))
	mux.HandleFunc("GET /api/v1/settings/defaults", a.auth(a.getDefaults))
	mux.HandleFunc("PUT /api/v1/settings/defaults", a.auth(a.putDefaults))
	mux.HandleFunc("GET /api/v1/jobs", a.placeholder("jobs"))
	mux.HandleFunc("GET /api/v1/experiments", a.placeholder("experiments"))
	mux.HandleFunc("GET /api/v1/chats", a.placeholder("chats"))
	mux.HandleFunc("POST /api/v1/admin/reload", a.auth(a.reload))
	a.serveUI(mux)
	return mux
}

// serveUI mounts the embedded web UI at / (the API keeps its /api prefix).
func (a *API) serveUI(mux *http.ServeMux) {
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		return // embed is compile-time; unreachable
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		fileServer.ServeHTTP(w, r)
	}))
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "deepthought-server",
		"version": a.Version,
		"uptime":  time.Since(a.Start).Round(time.Second).String(),
	})
}

func (a *API) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "deepthought-server",
		"version": a.Version,
		"dataDir": a.DataDir,
	})
}

// login exchanges the shared password for a session token. The username is
// supplied by the client and becomes the session identity (single-user phase).
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if a.Password == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "server auth not configured (set DEEPTHOUGHT_SERVER_PASSWORD)",
		})
		return
	}
	var req struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
		return
	}
	if req.User == "" || req.Password != a.Password {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid user or password"})
		return
	}
	token := a.Sessions.Login(req.User)
	if token == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "session issue failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  req.User,
	})
}

// crons serves the local cron tracking registry — the one real data endpoint
// in the skeleton, proving the fleet-aggregation story end to end.
func (a *API) crons(w http.ResponseWriter, r *http.Request) {
	reg, err := LoadCronRegistry(a.DataDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

// getDefaults serves the server settings layer.
func (a *API) getDefaults(w http.ResponseWriter, r *http.Request) {
	defaults, err := LoadDefaults(a.DataDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, defaults)
}

// putDefaults replaces the server settings layer (credential keys rejected).
func (a *API) putDefaults(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
		return
	}
	var defaults map[string]any
	if err := json.Unmarshal(body, &defaults); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be a JSON object"})
		return
	}
	if err := SaveDefaults(a.DataDir, defaults); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved"})
}

// reload genuinely re-reads the config and the skills packs — the HTTP form
// of live editing (SIGHUP is the signal form). Returns what it found.
func (a *API) reload(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"status": "reloaded"}
	if path, err := config.DefaultPath(); err == nil {
		if cfg, err := config.Load(path); err == nil {
			out["config"] = map[string]any{
				"providers": len(cfg.File.Providers),
				"models":    len(cfg.File.Models),
			}
		} else {
			out["config"] = map[string]any{"error": err.Error()}
		}
	}
	if list, err := skills.NewLoader().Load("."); err == nil {
		out["skills"] = len(list)
	} else {
		out["skills"] = 0
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) placeholder(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error":  "not implemented yet",
			"future": fmt.Sprintf("GET /api/v1/%s is part of the roadmap (docs/SERVER.md)", name),
		})
	}
}

// auth enforces the session token. Health endpoints stay open (probes inside
// the cluster); everything under /api requires a session. No password
// configured → 503, never silently open.
func (a *API) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.Password == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "server auth not configured (set DEEPTHOUGHT_SERVER_PASSWORD)",
			})
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="deepthought"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		if _, ok := a.Sessions.Valid(got); !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="deepthought"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
