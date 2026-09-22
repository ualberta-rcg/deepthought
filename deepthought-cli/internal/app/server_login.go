package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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
}
type ServerLoginResultMsg struct {
	Session *ServerSession
	Err     string
}
type serverSyncResultMsg struct{ Err string }
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
	req, err := http.NewRequest(method, strings.TrimRight(s.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("invalid server URL")
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := serverHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("server unavailable; changes remain saved locally")
	}
	defer resp.Body.Close()
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
		return ServerLoginResultMsg{Session: &ServerSession{URL: f.Server.URL, User: user, Token: token, Defaults: defaults, LoginTime: time.Now()}}
	}
}
func settingsSyncCmd(w *SettingsSync) tea.Cmd {
	return func() tea.Msg { return settingsSyncResultMsg{Worker: w, Status: w.Run()} }
}
func pushChatCmd(source history.ChatStoreSource, sess *ServerSession, id string) tea.Cmd {
	return func() tea.Msg {
		coll, err := source().GetCollective(id)
		if err == nil {
			err = sess.PushChat(coll)
		}
		if err != nil {
			return serverSyncResultMsg{Err: credential.Redact(err.Error())}
		}
		return serverSyncResultMsg{}
	}
}
