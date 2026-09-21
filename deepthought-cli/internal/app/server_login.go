package app

// Server login: phase-1 client side of the client↔server link. The splash's
// "Log in to server" choice performs the shared-password login, keeps the
// session token for the app's lifetime, and fetches the server's settings
// defaults (the "server" layer of config.ResolveLayers). The network work
// runs inside a tea.Cmd — never in the model path.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/config"
)

// ServerSession is a logged-in server connection held for the app lifetime.
type ServerSession struct {
	URL       string
	User      string
	Token     string
	Defaults  map[string]any
	LoginTime time.Time
}

// ServerLoginResultMsg carries the async login outcome back to the model.
type ServerLoginResultMsg struct {
	Session *ServerSession
	Err     string
}

// LoginToServer performs POST /api/v1/auth/login with the configured user and
// shared password, returning the session token.
func LoginToServer(cfg *config.ServerConfig) (user, token string, err error) {
	body, _ := json.Marshal(map[string]string{"user": cfg.User, "password": cfg.ExpandedPassword()})
	url := strings.TrimRight(cfg.URL, "/") + "/api/v1/auth/login"
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("reach server: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Token string `json:"token"`
		User  string `json:"user"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		return "", "", fmt.Errorf("server auth not configured")
	case resp.StatusCode != http.StatusOK:
		if out.Error != "" {
			return "", "", fmt.Errorf("login failed: %s", out.Error)
		}
		return "", "", fmt.Errorf("login failed: HTTP %d", resp.StatusCode)
	}
	if out.Token == "" {
		return "", "", fmt.Errorf("login failed: no session token")
	}
	return out.User, out.Token, nil
}

// FetchServerDefaults gets the server's settings-defaults layer.
func FetchServerDefaults(url, token string) (map[string]any, error) {
	req, err := http.NewRequest("GET", strings.TrimRight(url, "/")+"/api/v1/settings/defaults", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch defaults: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch defaults: HTTP %d", resp.StatusCode)
	}
	var defaults map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&defaults); err != nil {
		return nil, fmt.Errorf("fetch defaults: %w", err)
	}
	return defaults, nil
}

// serverLoginCmd is the splash's async login command.
func serverLoginCmd(live *Settings) tea.Cmd {
	return func() tea.Msg {
		f := live.Snapshot()
		if f.Server == nil || f.Server.URL == "" || f.Server.User == "" {
			return ServerLoginResultMsg{Err: `no server configured — add a "server" {url, user, password} section to ~/.deepthought/config.json`}
		}
		user, token, err := LoginToServer(f.Server)
		if err != nil {
			return ServerLoginResultMsg{Err: err.Error()}
		}
		defaults, err := FetchServerDefaults(f.Server.URL, token)
		if err != nil {
			return ServerLoginResultMsg{Err: err.Error()}
		}
		return ServerLoginResultMsg{Session: &ServerSession{
			URL:       f.Server.URL,
			User:      user,
			Token:     token,
			Defaults:  defaults,
			LoginTime: time.Now(),
		}}
	}
}
