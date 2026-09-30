package store

// MySQL-store integration tests. Guarded by $DEEPTHOUGHT_MYSQL_DSN: they skip
// when unset (builds are database-free) and run wherever a real server
// database is reachable. The golden test pushes the same graph through
// SQLiteStore and MySQLStore and compares the rehydrated collectives; the
// remaining tests cover tenant isolation and the settings revision conflict.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"deepthought-server/graph"
)

func mysqlDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DEEPTHOUGHT_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DEEPTHOUGHT_MYSQL_DSN not set")
	}
	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// buildGraph makes a small but complete collective: an incursion with a
// prompt, a transmission with text and a completed probe carrying a pattern.
func buildGraph(t *testing.T, store graph.ChatStore) *graph.Collective {
	t.Helper()
	coll, err := store.CreateCollective(graph.SpawnCollectiveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	coll.Title = "golden"
	inc := coll.StartIncursion("what is the answer?")
	inc.Status = graph.IncursionCompleted
	tx := &graph.Transmission{
		Vinculum: graph.Vinculum{ID: "tx-" + coll.ID, Kind: "transmission", SessionID: coll.SessionID, CollectiveID: coll.ID, ParentID: inc.ID, CreatedAt: time.Now()},
		Text:     "42",
	}
	inc.Transmissions = append(inc.Transmissions, tx)
	probe := &graph.Probe{
		Vinculum: graph.Vinculum{ID: "prb-" + coll.ID, Kind: "probe", SessionID: coll.SessionID, CollectiveID: coll.ID, ParentID: tx.ID, CreatedAt: time.Now()},
		WireID:   "bash", Name: "bash", Status: graph.ProbeCompleted, Result: graph.ResultView{Content: "ok"},
	}
	tx.Probes = append(tx.Probes, probe)
	pattern := &graph.Pattern{
		Vinculum: graph.Vinculum{ID: "pat-" + coll.ID, Kind: "pattern", SessionID: coll.SessionID, CollectiveID: coll.ID, ParentID: probe.ID, CreatedAt: time.Now()},
		Category: "env", Content: "the answer is 42",
	}
	probe.Patterns = append(probe.Patterns, pattern)
	if err := store.SaveCollective(coll); err != nil {
		t.Fatal(err)
	}
	return coll
}

func TestMySQLGraphRoundTrip(t *testing.T) {
	db := mysqlDB(t)
	my := ForUser(db, "golden-user")
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM interactions WHERE user_id='golden-user'`) })

	want := buildGraph(t, my)

	got, err := my.GetCollective(want.ID)
	if err != nil {
		t.Fatalf("mysql GetCollective: %v", err)
	}
	a, _ := json.Marshal(mysqlSummarize(want))
	b, _ := json.Marshal(mysqlSummarize(got))
	if string(a) != string(b) {
		t.Errorf("rehydrated collective differs:\n built: %s\n read:  %s", a, b)
	}

	sums, err := my.ListCollectives()
	if err != nil || len(sums) != 1 {
		t.Fatalf("ListCollectives = %v, %v; want 1 summary", sums, err)
	}
	if sums[0].Title != "golden" || sums[0].Incursions != 1 || sums[0].Messages != 2 {
		t.Errorf("summary = %+v", sums[0])
	}
	if _, err := my.UsageByModel(); err != nil {
		t.Errorf("UsageByModel: %v", err)
	}
}

// mysqlSummarize projects a collective to the comparable essentials (no
// IDs — the two stores mint independent collectives).
func mysqlSummarize(c *graph.Collective) map[string]any {
	out := map[string]any{"title": c.Title, "incursions": len(c.Incursions)} // no IDs: independent stores mint independent IDs
	if len(c.Incursions) > 0 {
		inc := c.Incursions[0]
		out["prompt"] = inc.Prompt
		out["status"] = inc.Status
		if len(inc.Transmissions) > 0 {
			out["text"] = inc.Transmissions[0].Text
			if len(inc.Transmissions[0].Probes) > 0 {
				p := inc.Transmissions[0].Probes[0]
				out["probe"] = []any{p.Name, p.Status.String(), len(p.Patterns)}
			}
		}
	}
	return out
}

func TestMySQLTenantIsolation(t *testing.T) {
	db := mysqlDB(t)
	alice := ForUser(db, "iso-alice")
	bob := ForUser(db, "iso-bob")
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM interactions WHERE user_id IN ('iso-alice','iso-bob')`)
		_, _ = db.Exec(`DELETE FROM records WHERE user_id IN ('iso-alice','iso-bob')`)
	})

	coll := buildGraph(t, alice)
	buildGraph(t, bob)

	if _, err := bob.GetCollective(coll.ID); err != sql.ErrNoRows {
		t.Errorf("bob read alice's collective: %v", err)
	}
	bobSums, err := bob.ListCollectives()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range bobSums {
		if s.ID == coll.ID {
			t.Error("bob's list contains alice's collective")
		}
	}
	// records are scoped too
	if err := alice.PutRecord("journal", "job-1", map[string]int{"revision": 1}); err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	if err := bob.GetRecord("journal", "job-1", &v); err != sql.ErrNoRows {
		t.Errorf("bob read alice's record: %v", err)
	}
	// Records are namespaced per user: bob's claim of the same kind/id makes
	// his own record and leaves alice's untouched.
	claimed, err := bob.ClaimRecord("journal", "job-1", map[string]int{"revision": 99})
	if err != nil || !claimed {
		t.Errorf("bob's claim in his own namespace = %v %v, want true", claimed, err)
	}
	if err := alice.GetRecord("journal", "job-1", &v); err != nil || v["revision"] != 1 {
		t.Errorf("alice's record after bob's claim = %v %v, want revision 1", v, err)
	}
	// ReplaceRecord with the wrong revision fails; with the right one succeeds.
	if err := alice.ReplaceRecord("journal", "job-1", 7, map[string]int{"revision": 8}); err == nil {
		t.Error("stale revision replace succeeded")
	}
	if err := alice.ReplaceRecord("journal", "job-1", 1, map[string]int{"revision": 2}); err != nil {
		t.Errorf("fresh revision replace failed: %v", err)
	}
}

// TestMySQLForeignIDRefused: ids are global, so a save that names another
// user's interaction id must fail (and roll back) instead of overwriting it.
func TestMySQLForeignIDRefused(t *testing.T) {
	db := mysqlDB(t)
	alice := ForUser(db, "own-alice")
	bob := ForUser(db, "own-bob")
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM interactions WHERE user_id IN ('own-alice','own-bob')`) })

	coll := buildGraph(t, alice)
	stolen := *coll.Incursions[0].Transmissions[0]
	stolen.Text = "overwritten by bob"
	hijack := graph.NewCollective(graph.SpawnCollectiveRequest{})
	inc := hijack.StartIncursion("hijack")
	stolen.CollectiveID, stolen.ParentID = hijack.ID, inc.ID
	inc.Transmissions = []*graph.Transmission{&stolen}
	if err := bob.SaveCollective(hijack); !errors.Is(err, ErrForeignID) {
		t.Fatalf("SaveCollective with alice's id = %v, want ErrForeignID", err)
	}
	if _, err := bob.GetCollective(hijack.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("failed save left bob's collective behind: %v", err)
	}
	got, err := alice.GetCollective(coll.ID)
	if err != nil {
		t.Fatal(err)
	}
	if text := got.Incursions[0].Transmissions[0].Text; text != "42" {
		t.Errorf("alice's transmission = %q, want 42", text)
	}
}

func TestMySQLGetDroneRoundTrip(t *testing.T) {
	db := mysqlDB(t)
	my := ForUser(db, "drone-user")
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM interactions WHERE user_id='drone-user'`) })

	coll := buildGraph(t, my)
	probe := coll.Incursions[0].Transmissions[0].Probes[0]
	d, err := my.GetDrone(context.Background(), probe.ID)
	if err != nil {
		t.Fatalf("GetDrone: %v", err)
	}
	if d.Kind != "probe" || d.Outcome != graph.OutcomeSuccess || d.ParentID != probe.ParentID {
		t.Errorf("drone = kind %q outcome %q parent %q", d.Kind, d.Outcome, d.ParentID)
	}
	if _, err := ForUser(db, "someone-else").GetDrone(context.Background(), probe.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("another user read the drone: %v", err)
	}
}

func TestMySQLSettingsRevision(t *testing.T) {
	db := mysqlDB(t)
	users := NewUserStore(db)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM user_settings WHERE user_id='settings-user'`) })

	if err := users.UpsertLogin("settings-user", "Settings User"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.GetSettings("settings-user"); err != sql.ErrNoRows {
		t.Fatalf("expected no rows, got %v", err)
	}
	rev, err := users.SetSettings("settings-user", map[string]any{"effort": "high"}, 0)
	if err != nil || rev != 1 {
		t.Fatalf("create = %d, %v", rev, err)
	}
	if _, err := users.SetSettings("settings-user", map[string]any{"effort": "low"}, 0); err != ErrSettingsRevision {
		t.Errorf("re-create should conflict, got %v", err)
	}
	if _, err := users.SetSettings("settings-user", map[string]any{"effort": "low"}, 99); err != ErrSettingsRevision {
		t.Errorf("stale revision should conflict, got %v", err)
	}
	rev, err = users.SetSettings("settings-user", map[string]any{"effort": "max"}, 1)
	if err != nil || rev != 2 {
		t.Fatalf("update = %d, %v", rev, err)
	}
	got, err := users.GetSettings("settings-user")
	if err != nil || got.Settings["effort"] != "max" || got.Revision != 2 {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if time.Since(got.UpdatedAt) > time.Minute {
		t.Errorf("updated_at suspiciously old: %v", got.UpdatedAt)
	}
}

func TestMySQLSessionsAndCredentials(t *testing.T) {
	db := mysqlDB(t)
	user := "store-sess-" + time.Now().Format("150405.000000000")
	sessions := NewSessionDB(db)
	var hash [32]byte
	copy(hash[:], []byte(user))
	now := time.Now().Truncate(time.Second)
	if err := sessions.PutSession(hash, SessionRecord{UserID: user, Name: user, Created: now, LastSeen: now, Expires: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, err := sessions.GetSession(hash)
	if err != nil || got.Name != user || !got.Expires.Equal(now.Add(-time.Minute)) {
		t.Fatalf("session round trip = %+v, %v", got, err)
	}
	if rows, _ := sessions.ListSessions(user); len(rows) != 1 || rows[0].ID != SessionID(hash) {
		t.Fatalf("list = %+v", rows)
	}
	if n, err := sessions.PurgeSessions(now); err != nil || n < 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if _, err := sessions.GetSession(hash); !errors.Is(err, ErrNoSession) {
		t.Fatalf("purged session = %v", err)
	}

	creds := NewCredentialDB(db)
	salt1, err := creds.Salt(user)
	salt2, _ := creds.Salt(user)
	if err != nil || len(salt1) != 32 || string(salt1) != string(salt2) {
		t.Fatal("salt must be created once and stay stable")
	}
	rec := StoredCredential{CredentialRow: CredentialRow{ID: "0123456789abcdef0123456789abcdef", Name: "Aleph", BaseURL: "https://a", Wire: "openai", Kind: CredentialSecret, Fingerprint: "f1"}, Nonce: []byte("123456789012"), Ciphertext: []byte("ct")}
	if err := creds.Put(user, rec); err != nil {
		t.Fatal(err)
	}
	rec.Fingerprint = "f2"
	if err := creds.Put(user, rec); err != nil {
		t.Fatal(err)
	}
	back, err := creds.Get(user, rec.ID)
	if err != nil || back.Fingerprint != "f2" || string(back.Ciphertext) != "ct" {
		t.Fatalf("credential round trip = %+v, %v", back, err)
	}
	if _, err := creds.Get(user+"-other", rec.ID); !errors.Is(err, ErrNoCredential) {
		t.Fatal("another user can read the credential")
	}
	if ok, _ := creds.Delete(user, rec.ID); !ok {
		t.Fatal("delete failed")
	}
}
