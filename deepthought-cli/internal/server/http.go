// HTTP surface of deepthought-server: health/version endpoints (open, for
// probes), bearer-token-protected /api/v1 routes, and documented 501
// placeholders for the roadmap surface. See docs/SERVER.md.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/skills"
)

// API is the server's state (wired by cmd/deepthought-server).
type API struct {
	Start   time.Time
	DataDir string
	Token   string
	Version string
}

// NewMux builds the full route table.
func NewMux(a *API) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.health)
	mux.HandleFunc("GET /api/v1/version", a.auth(a.version))
	mux.HandleFunc("GET /api/v1/crons", a.auth(a.crons))
	mux.HandleFunc("GET /api/v1/jobs", a.placeholder("jobs"))
	mux.HandleFunc("GET /api/v1/experiments", a.placeholder("experiments"))
	mux.HandleFunc("GET /api/v1/chats", a.placeholder("chats"))
	mux.HandleFunc("POST /api/v1/admin/reload", a.auth(a.reload))
	return mux
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

// auth enforces the bearer token. Health endpoints stay open (probes inside
// the cluster); everything under /api requires the token. No token configured
// → 503, never silently open.
func (a *API) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.Token == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "server auth not configured (set DEEPTHOUGHT_SERVER_TOKEN)",
			})
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" || got != a.Token {
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
