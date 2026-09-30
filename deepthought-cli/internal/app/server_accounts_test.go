package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/tui"
	"deepthought-cli/internal/unimatrix"
)

func accountRoot(t *testing.T) (RootModel, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	e := newCredentialEndpoint()
	e.add(config.Provider{Name: "alpha", BaseURL: "https://alpha.invalid/v1"}, "secret", "sk-alpha-server")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/api/v1/user/sessions") {
			now := time.Now()
			_ = json.NewEncoder(w).Encode(map[string]any{"current": "aaaa1111", "sessions": []serverSessionRow{
				{ID: "aaaa1111", Created: now, LastSeen: now, Expires: now.Add(time.Hour)},
				{ID: "bbbb2222", Created: now, LastSeen: now, Expires: now.Add(time.Hour)},
			}})
			return
		}
		e.serve(w, r)
	}))
	t.Cleanup(ts.Close)
	m := sidebarRoot(t, 120, 40)
	m.server = &ServerSession{URL: ts.URL, User: "fixture", Token: "t"}
	return m, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

func runAction(t *testing.T, m RootModel, a tui.WorkspaceAction) RootModel {
	t.Helper()
	next, cmd := m.workspaceAction(a)
	m = next.(RootModel)
	if cmd != nil {
		next, _ = m.Update(cmd())
		m = next.(RootModel)
	}
	return m
}

func TestServerKeysListIsMasked(t *testing.T) {
	m, _ := accountRoot(t)
	m = runAction(t, m, tui.WorkspaceAction{Kind: "server-credentials"})
	if m.workspace.Title != "Provider keys on server" || !strings.Contains(m.workspace.Notice, "Shared-password") {
		t.Fatalf("workspace = %q / %q", m.workspace.Title, m.workspace.Notice)
	}
	if len(m.workspace.Items) != 2 || !strings.Contains(m.workspace.Items[0].Label, "alpha") || m.workspace.Items[0].Kind != "delete-credential" {
		t.Fatalf("items = %+v", m.workspace.Items)
	}
	for _, it := range m.workspace.Items {
		if strings.Contains(it.Label+it.Detail, "sk-alpha") {
			t.Fatalf("key value shown: %+v", it)
		}
	}
}

func TestServerSessionsMarkCurrentAndRevoke(t *testing.T) {
	m, calls := accountRoot(t)
	m = runAction(t, m, tui.WorkspaceAction{Kind: "server-sessions"})
	items := m.workspace.Items
	if len(items) != 3 || !strings.Contains(items[0].Label, "(this client)") || items[0].Kind != "" || items[1].Kind != "revoke-session" {
		t.Fatalf("items = %+v", items)
	}
	m = runAction(t, m, tui.WorkspaceAction{Kind: "revoke-session", ID: "bbbb2222"})
	found := false
	for _, c := range calls() {
		found = found || c == "DELETE /api/v1/user/sessions/bbbb2222"
	}
	if !found {
		t.Fatalf("calls = %v, want a DELETE of the other session", calls())
	}
}

func TestDisconnectLogsOut(t *testing.T) {
	m, calls := accountRoot(t)
	next, cmd := m.workspaceAction(tui.WorkspaceAction{Kind: "disconnect-server"})
	if next.(RootModel).server != nil || cmd == nil {
		t.Fatal("disconnect should drop the session and return the logout call")
	}
	cmd()
	if got := strings.Join(calls(), ","); !strings.Contains(got, "POST /api/v1/auth/logout") {
		t.Fatalf("calls = %s, want a logout", got)
	}
}

func TestServerAccountsUnsupported(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	m := sidebarRoot(t, 120, 40)
	m.server = &ServerSession{URL: ts.URL, User: "fixture", Token: "t"}
	m = runAction(t, m, tui.WorkspaceAction{Kind: "server-credentials"})
	if !strings.Contains(m.workspace.Notice, "does not store provider keys") {
		t.Fatalf("notice = %q", m.workspace.Notice)
	}
	m = runAction(t, m, tui.WorkspaceAction{Kind: "server-sessions"})
	if !strings.Contains(m.workspace.Notice, "does not list sessions") {
		t.Fatalf("notice = %q", m.workspace.Notice)
	}
}

func TestRoleClientNamesMissingKey(t *testing.T) {
	m := sidebarRoot(t, 120, 40)
	f := m.deps.Live.Snapshot()
	f.Providers = []config.Provider{{Name: "aleph", BaseURL: "https://aleph.invalid/v1"}}
	f.Models = []unimatrix.Model{{ID: "m", Provider: "aleph", Capabilities: []unimatrix.Capability{unimatrix.CapChat, unimatrix.CapTools}, Context: 65536}}
	f.Roles = map[string]string{unimatrix.RoleAgentic: "m"}
	if err := m.deps.Live.Save(f); err != nil {
		t.Fatal(err)
	}
	_, _, err := m.deps.Live.RoleClient(unimatrix.RoleAgentic)
	var noKey NoKeyError
	if !errors.As(err, &noKey) || noKey.Provider != "aleph" || !strings.Contains(err.Error(), "Settings › Providers › aleph") {
		t.Fatalf("err = %v", err)
	}
	if msg := checkModelCmd(m.deps.Live)().(modelHealthMsg); strings.Contains(msg.reason, "F1") {
		t.Fatalf("health reason should be the actionable no-key text alone: %q", msg.reason)
	}
}

func TestSameEndpoint(t *testing.T) {
	p := config.Provider{Name: "aleph", BaseURL: "https://aleph.invalid/v1/"}
	if !sameEndpoint(p, "https://aleph.invalid/v1", "") || !sameEndpoint(p, "https://aleph.invalid/v1", "openai") {
		t.Fatal("trailing slash and default wire should match")
	}
	if sameEndpoint(p, "https://aleph.invalid/v1", "anthropic") || sameEndpoint(p, "https://other.invalid/v1", "openai") {
		t.Fatal("different wire or URL must not match")
	}
}
