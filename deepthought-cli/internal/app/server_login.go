package app

// Server login + sync: the client side of the client↔server link. The splash's
// "Log in to server" performs the shared-password login, keeps the session
// token for the app's lifetime, fetches the server's settings defaults, and
// runs the roving-settings sync (pull the user's document down — with a
// key-level change notice — or push local when the server has none). Chats
// push to the server after each completed turn. Network work runs inside tea
// commands — never in the model path.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
)

// ServerSession is a logged-in server connection held for the app lifetime.
type ServerSession struct {
	URL        string
	User       string
	Token      string
	Defaults   map[string]any // fleet defaults layer (the admin-served one)
	Settings   map[string]any // the user's roving document as last seen
	Revision   int64          // its revision at last sync
	LoginTime  time.Time
	SyncNotice string // human summary of what the login sync did
}

// ServerLoginResultMsg carries the async login outcome back to the model.
type ServerLoginResultMsg struct {
	Session *ServerSession
	Err     string
}

// serverSyncResultMsg reports an asynchronous post-login push (settings or
// chat); failures surface as a chat notice, never a block.
type serverSyncResultMsg struct{ Err string }

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

func (s *ServerSession) do(method, path string, body []byte, out any) error {
	req, err := http.NewRequest(method, strings.TrimRight(s.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// FetchServerDefaults gets the server's settings-defaults layer.
func FetchServerDefaults(url, token string) (map[string]any, error) {
	s := ServerSession{URL: url, Token: token}
	var defaults map[string]any
	if err := s.do("GET", "/api/v1/settings/defaults", nil, &defaults); err != nil {
		return nil, err
	}
	return defaults, nil
}

// fetchUserSettings GETs the roving document ({settings, revision}).
func (s *ServerSession) fetchUserSettings() (map[string]any, int64, error) {
	var out struct {
		Settings map[string]any `json:"settings"`
		Revision int64          `json:"revision"`
	}
	if err := s.do("GET", "/api/v1/user/settings", nil, &out); err != nil {
		return nil, 0, err
	}
	return out.Settings, out.Revision, nil
}

// pushUserSettings PUTs the roving document at the given base revision.
func (s *ServerSession) pushUserSettings(settings map[string]any, revision int64) (int64, error) {
	body, _ := json.Marshal(map[string]any{"settings": settings, "revision": revision})
	var out struct {
		Revision int64 `json:"revision"`
	}
	if err := s.do("PUT", "/api/v1/user/settings", body, &out); err != nil {
		return 0, err
	}
	return out.Revision, nil
}

// fileToMap projects a config File to a plain map for the wire.
func fileToMap(f config.File) map[string]any {
	raw, _ := json.Marshal(f)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// diffTopLevel lists top-level keys whose values differ between two maps.
func diffTopLevel(a, b map[string]any) []string {
	var diff []string
	for k, av := range a {
		if bv, ok := b[k]; !ok {
			diff = append(diff, "+"+k)
		} else {
			ra, _ := json.Marshal(av)
			rb, _ := json.Marshal(bv)
			if string(ra) != string(rb) {
				diff = append(diff, "~"+k)
			}
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			diff = append(diff, "-"+k)
		}
	}
	sort.Strings(diff)
	return diff
}

// syncSettings runs the login-time roving-settings sync:
//   - server empty      → push the local document up (first login from this
//     user's primary machine seeds the server)
//   - differs from local → server wins: pull down and return the changed keys
//     (the caller applies and shows the notice)
//   - identical          → nothing to do
func (s *ServerSession) syncSettings(live *Settings) (map[string]any, []string, error) {
	local := fileToMap(live.Snapshot())
	remote, revision, err := s.fetchUserSettings()
	if err != nil {
		return nil, nil, err
	}
	if remote == nil {
		rev, err := s.pushUserSettings(local, 0)
		if err != nil {
			return nil, nil, err
		}
		s.Settings, s.Revision = local, rev
		return local, nil, nil
	}
	s.Settings, s.Revision = remote, revision
	if diff := diffTopLevel(local, remote); len(diff) > 0 {
		return remote, diff, nil
	}
	return remote, nil, nil
}

// PushChat PUTs a full collective graph to the server (per-turn coarse sync).
func (s *ServerSession) PushChat(coll *history.Collective) error {
	body, err := json.Marshal(coll)
	if err != nil {
		return err
	}
	return s.do("PUT", "/api/v1/chats/"+coll.ID, body, nil)
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
		sess := &ServerSession{
			URL:       f.Server.URL,
			User:      user,
			Token:     token,
			Defaults:  defaults,
			LoginTime: time.Now(),
		}
		pulled, diff, err := sess.syncSettings(live)
		switch {
		case err != nil:
			sess.SyncNotice = "logged in as " + user + " (settings sync failed: " + err.Error() + ")"
		case len(diff) > 0:
			sess.SyncNotice = fmt.Sprintf("logged in as %s — pulled %d setting change(s) from server: %s", user, len(diff), strings.Join(diff, ", "))
		default:
			sess.SyncNotice = "logged in as " + user
		}
		_ = pulled
		return ServerLoginResultMsg{Session: sess}
	}
}

// pushChatCmd uploads one collective after a completed turn.
func pushChatCmd(source history.ChatStoreSource, sess *ServerSession, collectiveID string) tea.Cmd {
	return func() tea.Msg {
		coll, err := source().GetCollective(collectiveID)
		if err != nil {
			return serverSyncResultMsg{Err: "server chat sync: " + err.Error()}
		}
		if err := sess.PushChat(coll); err != nil {
			return serverSyncResultMsg{Err: "server chat sync: " + err.Error()}
		}
		return serverSyncResultMsg{}
	}
}

// pushSettingsCmd uploads the local document after a local edit (OnSave hook).
func pushSettingsCmd(live *Settings, sess *ServerSession) tea.Cmd {
	return func() tea.Msg {
		rev, err := sess.pushUserSettings(fileToMap(live.Snapshot()), sess.Revision)
		if err != nil {
			return serverSyncResultMsg{Err: "server settings sync: " + err.Error()}
		}
		sess.Revision = rev
		return serverSyncResultMsg{}
	}
}
