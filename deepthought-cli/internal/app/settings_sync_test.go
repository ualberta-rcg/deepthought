package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"deepthought-cli/internal/config"
)

type settingsEndpoint struct {
	mu           sync.Mutex
	doc          map[string]any
	revision     int64
	fail         bool
	conflictOnce bool
	beforeGet    func()
	gets         int
}

func (e *settingsEndpoint) serve(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail {
		w.WriteHeader(503)
		return
	}
	if r.Method == "GET" {
		e.gets++
		if e.beforeGet != nil && e.gets%2 == 0 {
			e.beforeGet()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"settings": e.doc, "revision": e.revision})
		return
	}
	var req struct {
		Settings map[string]any
		Revision int64
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		w.WriteHeader(400)
		return
	}
	if e.conflictOnce {
		e.conflictOnce = false
		e.revision++
		w.WriteHeader(409)
		return
	}
	if req.Revision != e.revision {
		w.WriteHeader(409)
		return
	}
	e.doc = req.Settings
	e.revision++
	_ = json.NewEncoder(w).Encode(map[string]any{"revision": e.revision})
}
func syncClient(t *testing.T, url string) *Settings {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEEPTHOUGHT_CLI_HOME", dir)
	cfg, err := config.OpenLocal(filepath.Join(dir, "config.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cfg.Local.Close() })
	live := NewSettings(cfg, filepath.Join(dir, "config.json"))
	f := live.Snapshot()
	f.Server = &config.ServerConfig{URL: url, User: "fixture"}
	if err = live.Save(f); err != nil {
		t.Fatal(err)
	}
	return live
}
func worker(live *Settings, url string) *SettingsSync {
	return live.SyncWorker(&ServerSession{URL: url, User: "fixture", Token: "synthetic-session"})
}
func TestSettingsSyncRoundTripRestartAndDeletion(t *testing.T) {
	endpoint := &settingsEndpoint{}
	server := httptest.NewServer(http.HandlerFunc(endpoint.serve))
	defer server.Close()
	a, b := syncClient(t, server.URL), syncClient(t, server.URL)
	f := a.Snapshot()
	f.Providers = []config.Provider{{Name: "alpha", BaseURL: "https://alpha.invalid/v1", APIKey: "synthetic-private-api-key"}}
	if err := a.Save(f); err != nil {
		t.Fatal(err)
	}
	wa, wb := worker(a, server.URL), worker(b, server.URL)
	if st := wa.Run(); st.State != "Synced" {
		t.Fatal(st)
	}
	if st := wb.Run(); st.State != "Synced" {
		t.Fatal(st)
	}
	if len(b.Snapshot().Providers) != 1 {
		t.Fatal("server provider never applied locally")
	}
	raw, _ := os.ReadFile(b.Path())
	if !strings.Contains(string(raw), "alpha") {
		t.Fatal("download absent from config mirror")
	}
	endpoint.mu.Lock()
	wire, _ := json.Marshal(endpoint.doc)
	endpoint.mu.Unlock()
	if strings.Contains(string(wire), "synthetic-private") || strings.Contains(string(wire), "api_key") {
		t.Fatal("secret uploaded")
	}
	f = b.Snapshot()
	f.Language = "fr"
	if err := b.Save(f); err != nil {
		t.Fatal(err)
	}
	if st := wb.Run(); st.State != "Synced" {
		t.Fatal(st)
	}
	f = b.Snapshot()
	f.Language = "de"
	if err := b.Save(f); err != nil {
		t.Fatal(err)
	}
	if st := wb.Run(); st.State != "Synced" {
		t.Fatal("second upload failed", st)
	}
	if st := wa.Run(); st.State != "Synced" {
		t.Fatal(st)
	}
	if a.Snapshot().Language != "de" || a.Snapshot().Providers[0].APIKey == "" {
		t.Fatal("pull lost settings or private binding")
	}
	f = b.Snapshot()
	f.Providers = nil
	if err := b.Save(f); err != nil {
		t.Fatal(err)
	}
	wb.Run()
	wa.Run()
	if len(a.Snapshot().Providers) != 0 {
		t.Fatal("deleted provider reappeared")
	}
	if err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	if a.Snapshot().Language != "de" {
		t.Fatal("restart lost sync")
	}
}
func TestSettingsSyncConflictsOfflineAndInFlightEdits(t *testing.T) {
	endpoint := &settingsEndpoint{conflictOnce: true}
	server := httptest.NewServer(http.HandlerFunc(endpoint.serve))
	defer server.Close()
	a, b := syncClient(t, server.URL), syncClient(t, server.URL)
	wa, wb := worker(a, server.URL), worker(b, server.URL)
	wa.Run()
	wb.Run()
	f := a.Snapshot()
	f.Language = "fr"
	a.Save(f)
	wa.Run()
	f = b.Snapshot()
	f.Language = "de"
	b.Save(f)
	st := wb.Run()
	if len(st.Conflicts) != 1 {
		t.Fatalf("expected conflict: %+v", st)
	}
	c := st.Conflicts[0]
	if err := wb.Resolve(c.Path, "remote"); err != nil {
		t.Fatal(err)
	}
	if st = wb.Run(); st.State != "Synced" || b.Snapshot().Language != "fr" {
		t.Fatal("resolution failed", st)
	}
	endpoint.mu.Lock()
	endpoint.fail = true
	endpoint.mu.Unlock()
	f = b.Snapshot()
	f.Language = "es"
	b.Save(f)
	if st = wb.Run(); st.State != "Needs attention" {
		t.Fatal("failure hidden")
	}
	// Recreate the worker to prove pending data and baseline survive process loss.
	b.syncWorkers = nil
	wb = worker(b, server.URL)
	endpoint.mu.Lock()
	endpoint.fail = false
	endpoint.gets = 0
	endpoint.beforeGet = func() {
		f := b.Snapshot()
		f.Language = "it"
		if err := b.Save(f); err != nil {
			t.Error(err)
		}
	}
	endpoint.mu.Unlock()
	wb.Run()
	if b.Snapshot().Language != "it" {
		t.Fatal("in-flight local edit lost")
	}
	endpoint.mu.Lock()
	endpoint.beforeGet = nil
	endpoint.mu.Unlock()
	if st = wb.Run(); st.State != "Synced" {
		t.Fatal("pending change not retried", st)
	}
}

func TestSettingsSyncDisconnectAndEndpointChange(t *testing.T) {
	remote := config.Defaults().File
	remote.Language = "fr"
	endpoint := &settingsEndpoint{doc: map[string]any(config.Shared(remote)), revision: 1}
	server := httptest.NewServer(http.HandlerFunc(endpoint.serve))
	defer server.Close()
	live := syncClient(t, server.URL)
	w := worker(live, server.URL)
	before := live.Snapshot().Language
	endpoint.beforeGet = w.Stop // disconnect while the readback is in flight
	w.Run()
	if live.Snapshot().Language != before {
		t.Fatal("disconnected transfer applied a late download")
	}
	endpoint.mu.Lock()
	gets := endpoint.gets
	endpoint.beforeGet = nil
	endpoint.mu.Unlock()
	w.Run()
	endpoint.mu.Lock()
	if endpoint.gets != gets {
		t.Error("disconnected worker contacted server")
	}
	endpoint.mu.Unlock()
	w = worker(live, server.URL)
	if st := w.Run(); st.State != "Synced" || live.Snapshot().Language != "fr" {
		t.Fatal("reconnect failed", st)
	}
	f := live.Snapshot()
	f.Server.URL = "https://other.invalid"
	if err := live.Save(f); err != nil {
		t.Fatal(err)
	}
	endpoint.mu.Lock()
	gets = endpoint.gets
	endpoint.mu.Unlock()
	if st := w.Run(); st.State != "Disconnected" {
		t.Fatal("manual sync ignored changed endpoint", st)
	}
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if endpoint.gets != gets {
		t.Fatal("changed endpoint still contacted old server")
	}
}
