package server

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"deepthought-server/store"
)

// dbAPI is a full server over the CI MySQL service (skips without a DSN).
func dbAPI(t *testing.T, withVault bool) (*API, *httptest.Server) {
	t.Helper()
	dsn := os.Getenv("DEEPTHOUGHT_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DEEPTHOUGHT_MYSQL_DSN not set")
	}
	db, users, err := OpenDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := &API{Start: time.Now(), DataDir: t.TempDir(), Password: "pw", Sessions: NewSessionStore(time.Hour), Version: "test",
		DB: db, Users: users, Credentials: store.NewCredentialDB(db)}
	a.Sessions.UseBackend(store.NewSessionDB(db))
	if withVault {
		if a.Vault, err = NewVault(testVaultKeyHex); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(NewMux(a))
	t.Cleanup(ts.Close)
	return a, ts
}

func uniqueUser(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()) }

func putCred(t *testing.T, ts *httptest.Server, token, id string, body map[string]string) int {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := authedDo(ts, "PUT", "/api/v1/user/credentials/"+id, token, raw)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

func TestCredentialSyncEndToEnd(t *testing.T) {
	_, ts := dbAPI(t, true)
	ada, bob := uniqueUser("ada"), uniqueUser("bob")
	adaTok, bobTok := loginSession(t, ts, ada, "pw"), loginSession(t, ts, bob, "pw")

	secretID := CredentialID("Aleph", "https://aleph.example/v1", "openai")
	envID := CredentialID("Local", "http://127.0.0.1:8000/v1", "openai")
	if code := putCred(t, ts, adaTok, secretID, map[string]string{"name": "Aleph", "base_url": "https://aleph.example/v1", "wire": "openai", "kind": "secret", "value": "sk-ada"}); code != 200 {
		t.Fatalf("put secret = %d", code)
	}
	if code := putCred(t, ts, adaTok, envID, map[string]string{"name": "Local", "base_url": "http://127.0.0.1:8000/v1", "wire": "openai", "kind": "env", "value": "$LOCAL_KEY"}); code != 200 {
		t.Fatalf("put env = %d", code)
	}
	if code := putCred(t, ts, adaTok, envID, map[string]string{"name": "Other", "base_url": "http://x", "wire": "openai", "kind": "env", "value": "$X"}); code != 400 {
		t.Fatalf("mismatched id = %d, want 400", code)
	}
	if code := putCred(t, ts, adaTok, envID, map[string]string{"name": "Local", "base_url": "http://127.0.0.1:8000/v1", "wire": "openai", "kind": "env", "value": "literal"}); code != 400 {
		t.Fatalf("env kind with a literal = %d, want 400", code)
	}

	resp, _ := authedGet(ts, "/api/v1/user/credentials", adaTok)
	var idx struct {
		Salt        string                `json:"salt"`
		Vault       bool                  `json:"vault"`
		Credentials []store.CredentialRow `json:"credentials"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&idx)
	if b, _ := json.Marshal(idx); strings.Contains(string(b), "sk-ada") {
		t.Fatal("index leaks a credential value")
	}
	salt, _ := hex.DecodeString(idx.Salt)
	if !idx.Vault || len(idx.Credentials) != 2 || len(salt) != 32 {
		t.Fatalf("index = %+v", idx)
	}
	for _, c := range idx.Credentials {
		if c.ID == secretID && c.Fingerprint != Fingerprint(salt, "sk-ada") {
			t.Fatal("client-computable fingerprint does not match")
		}
	}

	resp, _ = authedGet(ts, "/api/v1/user/credentials/"+secretID, adaTok)
	var one struct {
		Value string `json:"value"`
		Kind  string `json:"kind"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&one)
	if resp.StatusCode != 200 || one.Value != "sk-ada" || one.Kind != "secret" {
		t.Fatalf("get secret = %d %+v", resp.StatusCode, one)
	}

	if resp, _ := authedGet(ts, "/api/v1/user/credentials/"+secretID, bobTok); resp.StatusCode != 404 {
		t.Fatalf("bob reads ada's credential: %d", resp.StatusCode)
	}
	resp, _ = authedGet(ts, "/api/v1/user/credentials", bobTok)
	_ = json.NewDecoder(resp.Body).Decode(&idx)
	if len(idx.Credentials) != 0 {
		t.Fatalf("bob sees %d credentials", len(idx.Credentials))
	}

	if resp, _ := authedDo(ts, "DELETE", "/api/v1/user/credentials/"+secretID, adaTok, nil); resp.StatusCode != 200 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp, _ := authedGet(ts, "/api/v1/user/credentials/"+secretID, adaTok); resp.StatusCode != 404 {
		t.Fatalf("deleted credential still readable: %d", resp.StatusCode)
	}
}

func TestSecretCredentialsNeedVault(t *testing.T) {
	_, ts := dbAPI(t, false)
	tok := loginSession(t, ts, uniqueUser("novault"), "pw")
	id := CredentialID("Aleph", "https://aleph.example/v1", "openai")
	if code := putCred(t, ts, tok, id, map[string]string{"name": "Aleph", "base_url": "https://aleph.example/v1", "wire": "openai", "kind": "secret", "value": "sk"}); code != 503 {
		t.Fatalf("secret without vault = %d, want 503", code)
	}
	if code := putCred(t, ts, tok, id, map[string]string{"name": "Aleph", "base_url": "https://aleph.example/v1", "wire": "openai", "kind": "env", "value": "$ALEPH_KEY"}); code != 200 {
		t.Fatalf("env reference without vault = %d, want 200", code)
	}
}

// Sessions live in MySQL: a new server process accepts an old token.
func TestSessionsSurviveRestart(t *testing.T) {
	a, ts := dbAPI(t, false)
	tok := loginSession(t, ts, uniqueUser("restart"), "pw")
	fresh := NewSessionStore(time.Hour)
	fresh.UseBackend(store.NewSessionDB(a.DB))
	if _, ok := fresh.Valid(tok); !ok {
		t.Fatal("session lost across a restart")
	}
	fresh.Revoke(tok)
	if _, ok := a.Sessions.Valid(tok); ok {
		t.Fatal("revoked session still valid")
	}
}
