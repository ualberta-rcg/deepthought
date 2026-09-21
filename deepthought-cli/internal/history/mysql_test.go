package history

// MySQL-store integration tests. Guarded by $DEEPTHOUGHT_MYSQL_DSN: skipped
// when unset (local/Slurm runs without a server), active in CI where the
// workflow provides a mysql:8 service container. The golden test pushes the
// same graph through SQLiteStore and MySQLStore and compares the rehydrated
// collectives; the remaining tests cover tenant isolation and the settings
// revision conflict.

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"
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
func buildGraph(t *testing.T, store ChatStore) *Collective {
	t.Helper()
	coll, err := store.CreateCollective(SpawnCollectiveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	coll.Title = "golden"
	inc := coll.StartIncursion("what is the answer?")
	inc.Status = IncursionCompleted
	tx := &Transmission{
		Vinculum: Vinculum{ID: newID("tx"), Kind: "transmission", SessionID: coll.SessionID, CollectiveID: coll.ID, ParentID: inc.ID, CreatedAt: time.Now()},
		Text:     "42",
	}
	inc.Transmissions = append(inc.Transmissions, tx)
	probe := &Probe{
		Vinculum: Vinculum{ID: newID("prb"), Kind: "probe", SessionID: coll.SessionID, CollectiveID: coll.ID, ParentID: tx.ID, CreatedAt: time.Now()},
		WireID:   "bash", Name: "bash", Status: ProbeCompleted, Result: ResultView{Content: "ok"},
	}
	tx.Probes = append(tx.Probes, probe)
	pattern := &Pattern{
		Vinculum: Vinculum{ID: newID("pat"), Kind: "pattern", SessionID: coll.SessionID, CollectiveID: coll.ID, ParentID: probe.ID, CreatedAt: time.Now()},
		Category: "env", Content: "the answer is 42",
	}
	probe.Patterns = append(probe.Patterns, pattern)
	if err := store.SaveCollective(coll); err != nil {
		t.Fatal(err)
	}
	return coll
}

func TestMySQLGoldenAgainstSQLite(t *testing.T) {
	db := mysqlDB(t)

	sq, err := NewSQLiteStore(t.TempDir()+"/g.db", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sq.Close() })
	my := ForUser(db, "golden-user")
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM interactions WHERE user_id='golden-user'`) })

	want := buildGraph(t, sq)
	gotColl := buildGraph(t, my)

	got, err := my.GetCollective(gotColl.ID)
	if err != nil {
		t.Fatalf("mysql GetCollective: %v", err)
	}
	wantRt, err := sq.GetCollective(want.ID)
	if err != nil {
		t.Fatalf("sqlite GetCollective: %v", err)
	}
	a, _ := json.Marshal(mysqlSummarize(wantRt))
	b, _ := json.Marshal(mysqlSummarize(got))
	if string(a) != string(b) {
		t.Errorf("rehydrated collectives differ:\n sqlite: %s\n mysql:  %s", a, b)
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

// summarize projects a collective to the comparable essentials.
func mysqlSummarize(c *Collective) map[string]any {
	out := map[string]any{"title": c.Title, "id": c.ID, "incursions": len(c.Incursions)}
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
	claimed, err := bob.ClaimRecord("journal", "job-1", map[string]int{"revision": 1})
	if err != nil || claimed {
		t.Errorf("bob claimed alice's record: %v %v", claimed, err)
	}
	// ReplaceRecord with the wrong revision fails; with the right one succeeds.
	if err := alice.ReplaceRecord("journal", "job-1", 7, map[string]int{"revision": 8}); err == nil {
		t.Error("stale revision replace succeeded")
	}
	if err := alice.ReplaceRecord("journal", "job-1", 1, map[string]int{"revision": 2}); err != nil {
		t.Errorf("fresh revision replace failed: %v", err)
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
