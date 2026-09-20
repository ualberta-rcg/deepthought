package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"deepthought-cli/internal/cron"
)

func testAPI(token, dataDir string) (*API, *httptest.Server) {
	a := &API{Start: time.Now(), DataDir: dataDir, Token: token, Version: "test"}
	ts := httptest.NewServer(NewMux(a))
	return a, ts
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

func TestAuthGatesAPI(t *testing.T) {
	_, ts := testAPI("sekrit", t.TempDir())
	defer ts.Close()

	// No token → 401.
	resp, _ := http.Get(ts.URL + "/api/v1/version")
	if resp.StatusCode != 401 {
		t.Errorf("no token = %d, want 401", resp.StatusCode)
	}
	// Wrong token → 401.
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/version", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Errorf("wrong token = %d, want 401", resp.StatusCode)
	}
	// Right token → 200.
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/version", nil)
	req.Header.Set("Authorization", "Bearer sekrit")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Errorf("right token = %d, want 200", resp.StatusCode)
	}
}

// No token configured → /api is 503, never silently open.
func TestNoTokenRefusesAPI(t *testing.T) {
	_, ts := testAPI("", t.TempDir())
	defer ts.Close()
	resp, _ := http.Get(ts.URL + "/api/v1/version")
	if resp.StatusCode != 503 {
		t.Errorf("no-token api = %d, want 503", resp.StatusCode)
	}
}

func TestPlaceholders501(t *testing.T) {
	_, ts := testAPI("sekrit", t.TempDir())
	defer ts.Close()
	for _, path := range []string{"/api/v1/jobs", "/api/v1/experiments", "/api/v1/chats"} {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer sekrit")
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != 501 {
			t.Errorf("%s = %d, want 501", path, resp.StatusCode)
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
	_, ts := testAPI("sekrit", dir)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/crons", nil)
	req.Header.Set("Authorization", "Bearer sekrit")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("crons = %d", resp.StatusCode)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["host"] == nil {
		t.Error("registry body missing host field")
	}
}
