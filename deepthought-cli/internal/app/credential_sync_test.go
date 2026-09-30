package app

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/credential"
)

// credentialEndpoint fakes /api/v1/user/credentials.
type credentialEndpoint struct {
	mu     sync.Mutex
	salt   []byte
	vault  bool
	rows   map[string]serverCredential
	values map[string]string
	puts   map[string]string
}

func newCredentialEndpoint() *credentialEndpoint {
	return &credentialEndpoint{salt: make([]byte, 32), vault: true, rows: map[string]serverCredential{}, values: map[string]string{}, puts: map[string]string{}}
}

func (e *credentialEndpoint) add(p config.Provider, kind, value string) {
	id := credentialIDFor(p)
	e.rows[id] = serverCredential{ID: id, Name: p.Name, BaseURL: p.BaseURL, Wire: p.WireOrDefault(), Kind: kind, Fingerprint: config.CredentialFingerprint(e.salt, value)}
	e.values[id] = value
}

func (e *credentialEndpoint) serve(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/user/credentials")
	id = strings.TrimPrefix(id, "/")
	switch {
	case r.Method == "GET" && id == "":
		rows := []serverCredential{}
		for _, c := range e.rows {
			rows = append(rows, c)
		}
		_ = json.NewEncoder(w).Encode(credentialIndex{Salt: hex.EncodeToString(e.salt), Vault: e.vault, Credentials: rows})
	case r.Method == "GET":
		c, ok := e.rows[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"kind": c.Kind, "value": e.values[id]})
	case r.Method == "PUT":
		var req struct{ Name, Kind, Value string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		e.puts[id] = req.Kind + ":" + req.Value
	}
}

func credentialFixture(t *testing.T) (*Settings, *ServerSession, *credentialEndpoint) {
	t.Helper()
	e := newCredentialEndpoint()
	ts := httptest.NewServer(http.HandlerFunc(e.serve))
	t.Cleanup(ts.Close)
	live := syncClient(t, ts.URL)
	off := false
	f := live.Snapshot()
	f.Providers = []config.Provider{
		{Name: "alpha", BaseURL: "https://alpha.invalid/v1", APIKey: "sk-alpha-local"},
		{Name: "beta", BaseURL: "https://beta.invalid/v1"},
		{Name: "gamma", BaseURL: "https://gamma.invalid/v1", APIKey: "$GAMMA_KEY"},
		{Name: "delta", BaseURL: "https://delta.invalid/v1", APIKey: "sk-delta-private", SyncCredential: &off},
	}
	if err := live.Save(f); err != nil {
		t.Fatal(err)
	}
	e.add(f.Providers[1], "secret", "sk-beta-server")
	e.add(f.Providers[2], "env", "$GAMMA_KEY")
	return live, &ServerSession{URL: ts.URL, User: "fixture", Token: "t"}, e
}

func TestCredentialPlanAndApply(t *testing.T) {
	live, sess, e := credentialFixture(t)
	idx, err := sess.credentialIndex()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := live.Saved()
	items := planCredentials(saved, idx)
	states := map[string]string{}
	for _, it := range items {
		states[it.Provider.Name] = it.State
	}
	if len(items) != 2 || states["alpha"] != credUpload || states["beta"] != credDownload {
		t.Fatalf("plan = %v (gamma matches, delta opted out)", states)
	}
	if _, err := applyCredentials(live, sess, items); err != nil {
		t.Fatal(err)
	}
	if got := e.puts[credentialIDFor(saved.Providers[0])]; got != "secret:sk-alpha-local" {
		t.Fatalf("alpha upload = %q", got)
	}
	for id, v := range e.puts {
		if strings.Contains(v, "delta") {
			t.Fatalf("opted-out key uploaded (%s)", id)
		}
	}
	after, _ := live.Saved()
	beta := after.Providers[1]
	if !strings.HasPrefix(beta.APIKey, "secret:") || credential.Resolve(beta.APIKey) != "sk-beta-server" {
		t.Fatalf("beta binding = %q", beta.APIKey)
	}
	if strings.Contains(credential.Redact("key sk-beta-server"), "sk-beta-server") {
		t.Fatal("downloaded key is not redacted")
	}
}

func TestCredentialConflictNeedsAChoice(t *testing.T) {
	live, sess, e := credentialFixture(t)
	saved, _ := live.Saved()
	e.add(saved.Providers[2], "env", "$OTHER_KEY")
	idx, _ := sess.credentialIndex()
	var conflict *credItem
	for _, it := range planCredentials(saved, idx) {
		if it.Provider.Name == "gamma" {
			it := it
			conflict = &it
		}
	}
	if conflict == nil || conflict.State != credConflict || conflict.Choice != credChoiceSkip {
		t.Fatalf("gamma = %+v, want a skipped conflict until reviewed", conflict)
	}
	picked := applyReviewChoices([]credItem{*conflict}, []int{2})
	if picked[0].Choice != credChoiceDownload {
		t.Fatal("'Take server' must download")
	}
	rows := credentialReviewRows([]credItem{*conflict})
	if len(rows[0].Options) != 3 || !strings.Contains(rows[0].Detail, "$GAMMA_KEY") {
		t.Fatalf("review row = %+v", rows[0])
	}
}

func TestCredentialReviewOnceThenAuto(t *testing.T) {
	live, sess, e := credentialFixture(t)
	msg := credentialCheckCmd(live, sess)().(credentialPlanMsg)
	if msg.Err != nil || len(msg.Review) != 2 {
		t.Fatalf("first check = %+v, want both items for review", msg)
	}
	for _, row := range credentialReviewRows(msg.Review) {
		if strings.Contains(row.Detail, "sk-") {
			t.Fatalf("review shows a key: %q", row.Detail)
		}
	}
	applied := credentialApplyCmd(live, sess, applyReviewChoices(msg.Review, []int{1, 1}))().(credentialAppliedMsg)
	if applied.Err != nil || len(e.puts) != 0 {
		t.Fatalf("skip-all applied %+v, puts %v", applied, e.puts)
	}
	msg = credentialCheckCmd(live, sess)().(credentialPlanMsg)
	if msg.Err != nil || len(msg.Review) != 0 || !strings.Contains(msg.Summary, "1 uploaded") || !strings.Contains(msg.Summary, "1 downloaded") {
		t.Fatalf("after review = %+v, want automatic apply", msg)
	}
}

func TestCredentialSyncUnsupportedServer(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	live := syncClient(t, ts.URL)
	msg := credentialCheckCmd(live, &ServerSession{URL: ts.URL, User: "fixture", Token: "t"})().(credentialPlanMsg)
	if msg.Err != nil || len(msg.Review) != 0 {
		t.Fatalf("older server = %+v, want a silent no-op", msg)
	}
}
