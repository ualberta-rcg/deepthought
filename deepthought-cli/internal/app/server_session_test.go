package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"deepthought-cli/internal/history"
)

// tokenServer accepts only the bearer token in *valid; every request is counted.
func tokenServer(valid *atomic.Value, status *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+valid.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if code := status.Load(); code != 0 {
			w.WriteHeader(int(code))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
}

func TestServer401RenewsOnce(t *testing.T) {
	var valid atomic.Value
	var status atomic.Int32
	valid.Store("fresh")
	srv := tokenServer(&valid, &status)
	defer srv.Close()

	var logins int
	sess := &ServerSession{URL: srv.URL, auth: &sessionAuth{token: "stale", relogin: func() (string, error) {
		logins++
		return "fresh", nil
	}}}
	clone := *sess // the sync worker works on a copy; the renewed token must be shared
	if err := clone.do("GET", "/api/v1/user/settings", nil, nil); err != nil {
		t.Fatalf("request after renewal = %v", err)
	}
	if err := sess.do("GET", "/api/v1/user/settings", nil, nil); err != nil || logins != 1 {
		t.Fatalf("second request = %v after %d logins, want nil after 1", err, logins)
	}
}

func TestServer401WithoutRenewalExpires(t *testing.T) {
	var valid atomic.Value
	var status atomic.Int32
	valid.Store("server-restarted")
	srv := tokenServer(&valid, &status)
	defer srv.Close()

	for name, sess := range map[string]*ServerSession{
		"no relogin":     {URL: srv.URL, Token: "old"},
		"relogin fails":  {URL: srv.URL, auth: &sessionAuth{token: "old", relogin: func() (string, error) { return "", errors.New("no password bound") }}},
		"still rejected": {URL: srv.URL, auth: &sessionAuth{token: "old", relogin: func() (string, error) { return "also-wrong", nil }}},
	} {
		if err := sess.do("GET", "/api/v1/user/settings", nil, nil); !errors.Is(err, ErrSessionExpired) {
			t.Errorf("%s: err = %v, want ErrSessionExpired", name, err)
		}
	}
}

func TestSettingsSyncStopsOnExpiredSession(t *testing.T) {
	var valid atomic.Value
	var status atomic.Int32
	valid.Store("nobody-has-this")
	srv := tokenServer(&valid, &status)
	defer srv.Close()
	live := syncClient(t, srv.URL)
	if st := worker(live, srv.URL).Run(); st.State != syncStateExpired {
		t.Fatalf("sync status = %+v, want %q", st, syncStateExpired)
	}
}

func TestFailedChatPushIsRetriedDurably(t *testing.T) {
	var valid atomic.Value
	var status atomic.Int32
	valid.Store("tok")
	status.Store(http.StatusServiceUnavailable)
	srv := tokenServer(&valid, &status)
	defer srv.Close()

	live := syncClient(t, srv.URL)
	store, err := history.NewSQLiteStore(filepath.Join(t.TempDir(), "history.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	coll, err := store.CreateCollective(history.SpawnCollectiveRequest{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	source := func() history.ChatStore { return store }
	sess := &ServerSession{URL: srv.URL, User: "fixture", Token: "tok"}

	if err := pushOneChat(source, sess, live.LocalStore(), coll.ID); err == nil {
		t.Fatal("push to an unavailable server succeeded")
	}
	if raws, _ := live.LocalStore().Records(pendingChatKind); len(raws) != 1 {
		t.Fatalf("pending records = %d, want 1", len(raws))
	}
	status.Store(0)
	if err := flushPendingChats(source, sess, live.LocalStore()); err != nil {
		t.Fatalf("flush = %v", err)
	}
	if raws, _ := live.LocalStore().Records(pendingChatKind); len(raws) != 0 {
		t.Fatalf("pending records after flush = %d, want 0", len(raws))
	}
}
