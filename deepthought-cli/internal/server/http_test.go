package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"deepthought-cli/internal/cron"
)

func testAPI(password, dataDir string) (*API, *httptest.Server) {
	a := &API{Start: time.Now(), DataDir: dataDir, Password: password, Sessions: NewSessionStore(time.Hour), Version: "test"}
	ts := httptest.NewServer(NewMux(a))
	return a, ts
}

// loginSession performs the login flow and returns the bearer token.
func loginSession(t *testing.T, ts *httptest.Server, user, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"user": user, "password": password})
	resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	var out struct {
		Token string `json:"token"`
		User  string `json:"user"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d, want 200", resp.StatusCode)
	}
	return out.Token
}

func authedGet(ts *httptest.Server, path, token string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultClient.Do(req)
}

func TestHealthOpen(t *testing.T) {
	_, ts := testAPI("", t.TempDir())
	defer ts.Close()
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, err, resp)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if body["status"] != "ok" {
			t.Errorf("%s status = %v", path, body["status"])
		}
	}
}

func TestLoginAndAuthGate(t *testing.T) {
	_, ts := testAPI("Parad0x-test", t.TempDir())
	defer ts.Close()

	// No token → 401.
	resp, _ := http.Get(ts.URL + "/api/v1/version")
	if resp.StatusCode != 401 {
		t.Errorf("no token = %d, want 401", resp.StatusCode)
	}
	// Wrong password → 401, no session.
	body, _ := json.Marshal(map[string]string{"user": "ada", "password": "nope"})
	resp, _ = http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if resp.StatusCode != 401 {
		t.Errorf("wrong password = %d, want 401", resp.StatusCode)
	}
	// Missing user → 401.
	body, _ = json.Marshal(map[string]string{"password": "Parad0x-test"})
	resp, _ = http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if resp.StatusCode != 401 {
		t.Errorf("missing user = %d, want 401", resp.StatusCode)
	}
	// Right password → session token → 200 on protected route.
	token := loginSession(t, ts, "ada", "Parad0x-test")
	resp, err := authedGet(ts, "/api/v1/version", token)
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("session token = %v %v, want 200", err, resp)
	}
	// Bogus token → 401.
	resp, err = authedGet(ts, "/api/v1/version", "deadbeef")
	if err != nil || resp.StatusCode != 401 {
		t.Errorf("bogus token = %v %v, want 401", err, resp)
	}
}

// No password configured → /api is 503, never silently open (login included).
func TestNoPasswordRefusesAPI(t *testing.T) {
	_, ts := testAPI("", t.TempDir())
	defer ts.Close()
	resp, _ := http.Get(ts.URL + "/api/v1/version")
	if resp.StatusCode != 503 {
		t.Errorf("no-password api = %d, want 503", resp.StatusCode)
	}
	body, _ := json.Marshal(map[string]string{"user": "ada", "password": "x"})
	resp, _ = http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if resp.StatusCode != 503 {
		t.Errorf("no-password login = %d, want 503", resp.StatusCode)
	}
}

func TestPlaceholders501(t *testing.T) {
	_, ts := testAPI("pw", t.TempDir())
	defer ts.Close()
	token := loginSession(t, ts, "ada", "pw")
	for _, path := range []string{"/api/v1/jobs", "/api/v1/experiments"} {
		resp, err := authedGet(ts, path, token)
		if err != nil || resp.StatusCode != 501 {
			t.Errorf("%s = %v %v, want 501", path, err, resp)
		}
	}
}

// /api/v1/chats is real now: without a database configured it answers 503
// (like the rest of the DB-backed surface), not a placeholder 501.
func TestDBEndpoints503WithoutDSN(t *testing.T) {
	_, ts := testAPI("pw", t.TempDir())
	defer ts.Close()
	token := loginSession(t, ts, "ada", "pw")
	for _, path := range []string{"/api/v1/chats", "/api/v1/user/settings"} {
		resp, err := authedGet(ts, path, token)
		if err != nil || resp.StatusCode != 503 {
			t.Errorf("%s = %v %v, want 503", path, err, resp)
		}
	}
}

func TestCronsEndpointServesRegistry(t *testing.T) {
	dir := t.TempDir()
	// Seed a registry through the cron package's own save path.
	reg, _ := LoadCronRegistry(dir)
	reg.Entries = append(reg.Entries, cron.EntryRecord{Hash: "abc123", Schedule: "0 9 * * *", Command: "run.sh"})
	if err := reg.Save(dir + "/cron"); err != nil {
		t.Fatal(err)
	}
	_, ts := testAPI("pw", dir)
	defer ts.Close()

	resp, err := authedGet(ts, "/api/v1/crons", loginSession(t, ts, "ada", "pw"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("crons = %v %v, want 200", err, resp)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["host"] == nil {
		t.Error("registry body missing host field")
	}
}

func TestSettingsDefaultsRoundTrip(t *testing.T) {
	_, ts := testAPI("pw", t.TempDir())
	defer ts.Close()
	token := loginSession(t, ts, "ada", "pw")

	// Absent defaults → empty object.
	resp, _ := authedGet(ts, "/api/v1/settings/defaults", token)
	if resp.StatusCode != 200 {
		t.Fatalf("get defaults = %d, want 200", resp.StatusCode)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 0 {
		t.Errorf("absent defaults = %v, want empty", got)
	}

	// PUT + GET round-trip.
	put, _ := json.Marshal(map[string]any{"effort": "high", "roles": map[string]any{"summary": "m1"}})
	req, _ := http.NewRequest("PUT", ts.URL+"/api/v1/settings/defaults", bytes.NewReader(put))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("put defaults = %d, want 200", resp.StatusCode)
	}
	resp, _ = authedGet(ts, "/api/v1/settings/defaults", token)
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["effort"] != "high" {
		t.Errorf("round-trip effort = %v, want high", got["effort"])
	}

	// Credential keys are rejected.
	put, _ = json.Marshal(map[string]any{"providers": []any{map[string]any{"name": "x", "api_key": "sk-1"}}})
	req, _ = http.NewRequest("PUT", ts.URL+"/api/v1/settings/defaults", bytes.NewReader(put))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 400 {
		t.Errorf("credential defaults = %d, want 400", resp.StatusCode)
	}
}

func TestUIServedAtRoot(t *testing.T) {
	_, ts := testAPI("pw", t.TempDir())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("root = %v %v, want 200", err, resp)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("root content-type = %q", ct)
	}
	// Unknown API path → JSON 404, not the UI.
	resp, err = http.Get(ts.URL + "/api/v1/nope")
	if err != nil || resp.StatusCode != 404 {
		t.Errorf("unknown api = %v %v, want 404", err, resp)
	}
}

func TestSessionExpiry(t *testing.T) {
	a := &API{Start: time.Now(), DataDir: t.TempDir(), Password: "pw", Sessions: NewSessionStore(time.Millisecond), Version: "test"}
	ts := httptest.NewServer(NewMux(a))
	defer ts.Close()
	token := loginSession(t, ts, "ada", "pw")
	time.Sleep(5 * time.Millisecond)
	resp, err := authedGet(ts, "/api/v1/version", token)
	if err != nil || resp.StatusCode != 401 {
		t.Errorf("expired session = %v %v, want 401", err, resp)
	}
}
