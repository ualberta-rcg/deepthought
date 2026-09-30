package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"charm.land/bubbletea/v2"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/credential"
	"deepthought-cli/internal/history"
)

type ServerSession struct {
	URL, User, Token string
	Defaults         map[string]any
	LoginTime        time.Time
	ctx              context.Context
	// auth is shared by every copy of the session (the sync worker copies
	// it), so a token renewed by one request is used by all.
	auth *sessionAuth
}

// sessionAuth holds the live token and how to renew it once after a 401.
type sessionAuth struct {
	mu      sync.Mutex
	token   string
	relogin func() (string, error)
}

// ErrSessionExpired means the server rejected the session and an automatic
// re-login was not possible; the client stops syncing until reconnected.
var ErrSessionExpired = errors.New("server session expired — reconnect in Settings › Server")

type ServerLoginResultMsg struct {
	Session *ServerSession
	Err     string
}
type serverSyncResultMsg struct {
	Err     string
	Expired bool
}

func (s *ServerSession) token() string {
	if s.auth == nil {
		return s.Token
	}
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	return s.auth.token
}

// renew performs the single automatic re-login allowed after a 401. It is
// skipped when another request already renewed the token (stale != current).
func (s *ServerSession) renew(stale string) bool {
	if s.auth == nil || s.auth.relogin == nil {
		return false
	}
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	if s.auth.token != stale {
		return true
	}
	tok, err := s.auth.relogin()
	if err != nil || tok == "" {
		return false
	}
	s.auth.token = tok
	return true
}

type settingsSyncResultMsg struct {
	Worker *SettingsSync
	Status SyncStatus
}
type serverHTTPError int

func (e serverHTTPError) Error() string { return fmt.Sprintf("server returned HTTP %d", int(e)) }
func isRevisionConflict(err error) bool {
	var code serverHTTPError
	return errors.As(err, &code) && int(code) == http.StatusConflict
}
func serverHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func LoginToServer(cfg *config.ServerConfig) (user, token string, err error) {
	body, _ := json.Marshal(map[string]string{"user": cfg.User, "password": cfg.ExpandedPassword()})
	resp, err := serverHTTPClient().Post(strings.TrimRight(cfg.URL, "/")+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("server unavailable; check the connection and retry")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", serverHTTPError(resp.StatusCode)
	}
	var out struct{ Token, User string }
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out) != nil || out.Token == "" {
		return "", "", fmt.Errorf("server returned an invalid login response")
	}
	if out.User == "" {
		out.User = cfg.User
	}
	credential.Register("server-session:"+cfg.URL, out.Token)
	return out.User, out.Token, nil
}
func (s *ServerSession) do(method, path string, body []byte, out any) error {
	resp, err := s.send(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrSessionExpired
	}
	if resp.StatusCode >= 300 {
		return serverHTTPError(resp.StatusCode)
	}
	if out != nil {
		if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
			return fmt.Errorf("invalid server response")
		}
	}
	return nil
}

// send issues the request, renewing the session once on a 401.
func (s *ServerSession) send(method, path string, body []byte) (*http.Response, error) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for attempt := 0; ; attempt++ {
		tok := s.token()
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.URL, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("invalid server URL")
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := serverHTTPClient().Do(req)
		if err != nil {
			return nil, fmt.Errorf("server unavailable; changes remain saved locally")
		}
		if resp.StatusCode != http.StatusUnauthorized || attempt > 0 || !s.renew(tok) {
			return resp, nil
		}
		resp.Body.Close()
	}
}

func FetchServerDefaults(url, token string) (map[string]any, error) {
	s := ServerSession{URL: url, Token: token}
	var out map[string]any
	err := s.do("GET", "/api/v1/settings/defaults", nil, &out)
	return out, err
}
func (s *ServerSession) fetchUserSettings() (map[string]any, int64, error) {
	var out struct {
		Settings map[string]any `json:"settings"`
		Revision int64          `json:"revision"`
	}
	err := s.do("GET", "/api/v1/user/settings", nil, &out)
	return out.Settings, out.Revision, err
}
func (s *ServerSession) pushUserSettings(settings map[string]any, revision int64) (int64, error) {
	body, _ := json.Marshal(map[string]any{"settings": settings, "revision": revision})
	if len(body) > 1<<20 {
		return 0, fmt.Errorf("shared settings exceed server size limit")
	}
	var out struct {
		Revision int64 `json:"revision"`
	}
	err := s.do("PUT", "/api/v1/user/settings", body, &out)
	return out.Revision, err
}
func (s *ServerSession) PushChat(coll *history.Collective) error {
	body, err := json.Marshal(coll)
	if err != nil {
		return err
	}
	return s.do("PUT", "/api/v1/chats/"+coll.ID, body, nil)
}
func serverLoginCmd(live *Settings) tea.Cmd {
	return func() tea.Msg {
		f := live.Snapshot()
		if f.Server == nil || f.Server.URL == "" || f.Server.User == "" {
			return ServerLoginResultMsg{Err: "Configure URL, user and password in Settings → Server."}
		}
		user, token, err := LoginToServer(f.Server)
		if err != nil {
			return ServerLoginResultMsg{Err: err.Error()}
		}
		defaults, _ := FetchServerDefaults(f.Server.URL, token)
		url, name := f.Server.URL, f.Server.User
		relogin := func() (string, error) {
			cur := live.Snapshot().Server
			if cur == nil || cur.URL != url || cur.User != name || cur.ExpandedPassword() == "" {
				return "", ErrSessionExpired
			}
			_, tok, err := LoginToServer(cur)
			return tok, err
		}
		return ServerLoginResultMsg{Session: &ServerSession{URL: url, User: user, Token: token, Defaults: defaults, LoginTime: time.Now(),
			auth: &sessionAuth{token: token, relogin: relogin}}}
	}
}
func settingsSyncCmd(w *SettingsSync) tea.Cmd {
	return func() tea.Msg { return settingsSyncResultMsg{Worker: w, Status: w.Run()} }
}

// pendingChatPush is the durable retry record for a chat that failed to
// reach the server; it is retried after the next login and after every
// successful push.
type pendingChatPush struct {
	URL, User, ID string
	Attempts      int
	LastError     string
}

const pendingChatKind = "chat-push-pending"

func pendingChatKey(sess *ServerSession, id string) string {
	return sess.URL + "|" + sess.User + "|" + id
}

func pushChatCmd(source history.ChatStoreSource, sess *ServerSession, store *config.LocalStore, id string) tea.Cmd {
	return func() tea.Msg {
		err := pushOneChat(source, sess, store, id)
		if err != nil {
			return serverSyncResultMsg{Err: "Chat not synced (will retry): " + credential.Redact(err.Error()), Expired: errors.Is(err, ErrSessionExpired)}
		}
		if err := flushPendingChats(source, sess, store); err != nil {
			return serverSyncResultMsg{Err: "Earlier chats not synced (will retry): " + credential.Redact(err.Error()), Expired: errors.Is(err, ErrSessionExpired)}
		}
		return serverSyncResultMsg{}
	}
}

// pushOneChat pushes one collective, recording or clearing its retry record.
func pushOneChat(source history.ChatStoreSource, sess *ServerSession, store *config.LocalStore, id string) error {
	coll, err := source().GetCollective(id)
	if err == nil {
		err = sess.PushChat(coll)
	}
	if store == nil {
		return err
	}
	key := pendingChatKey(sess, id)
	if err == nil {
		_ = store.DeleteRecord(pendingChatKind, key)
		return nil
	}
	var rec pendingChatPush
	_ = store.ReadRecord(pendingChatKind, key, &rec)
	rec.URL, rec.User, rec.ID = sess.URL, sess.User, id
	rec.Attempts++
	rec.LastError = credential.Redact(err.Error())
	_ = store.WriteRecord(pendingChatKind, key, rec)
	return err
}

// flushPendingChats retries every recorded chat for this server identity,
// stopping at the first failure (the rest stay recorded).
func flushPendingChats(source history.ChatStoreSource, sess *ServerSession, store *config.LocalStore) error {
	if store == nil {
		return nil
	}
	raws, err := store.Records(pendingChatKind)
	if err != nil {
		return err
	}
	for _, raw := range raws {
		var rec pendingChatPush
		if json.Unmarshal(raw, &rec) != nil || rec.URL != sess.URL || rec.User != sess.User || rec.ID == "" {
			continue
		}
		if err := pushOneChat(source, sess, store, rec.ID); err != nil {
			return err
		}
	}
	return nil
}

func flushPendingChatsCmd(source history.ChatStoreSource, sess *ServerSession, store *config.LocalStore) tea.Cmd {
	return func() tea.Msg {
		if err := flushPendingChats(source, sess, store); err != nil {
			return serverSyncResultMsg{Err: "Earlier chats not synced (will retry): " + credential.Redact(err.Error()), Expired: errors.Is(err, ErrSessionExpired)}
		}
		return serverSyncResultMsg{}
	}
}
