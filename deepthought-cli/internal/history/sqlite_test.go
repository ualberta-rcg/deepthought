package history

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepthought-cli/internal/babel"
)

func TestSQLiteStoreRoundTripAndIdempotency(t *testing.T) {
	root := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(root, "history.db"), filepath.Join(root, "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	drone, _ := NewDrone("hail", "session", map[string]string{"text": "hello"})
	drone.Sensitivity = SensitivityInternal
	if err := store.SaveDrone(context.Background(), drone, nil); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetDrone(context.Background(), drone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Body, drone.Body) || got.Sensitivity != SensitivityInternal {
		t.Fatalf("round trip = %+v", got)
	}
	duplicate, _ := NewDrone("hail", "session", map[string]string{"text": "hello"})
	if err := store.SaveDrone(context.Background(), duplicate, nil); err != nil {
		t.Fatalf("duplicate should be idempotent: %v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM interactions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestSQLiteStoreSpillsLargeBodies(t *testing.T) {
	root := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(root, "history.db"), filepath.Join(root, "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	drone, _ := NewDrone("probe_result", "session", map[string]string{
		"output": strings.Repeat("x", InlineBodyLimit+1),
	})
	if err := store.SaveDrone(context.Background(), drone, nil); err != nil {
		t.Fatal(err)
	}
	var body []byte
	var ref string
	if err := store.db.QueryRow(`SELECT body,body_ref FROM interactions WHERE id=?`, drone.ID).Scan(&body, &ref); err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 || ref == "" {
		t.Fatalf("body=%d ref=%q", len(body), ref)
	}
	info, err := os.Stat(filepath.Join(root, "bodies", ref))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("spill mode=%o", info.Mode().Perm())
	}
	got, err := store.GetDrone(context.Background(), drone.ID)
	if err != nil || !bytes.Equal(got.Body, drone.Body) {
		t.Fatalf("spilled round trip err=%v", err)
	}
}

// TestSQLiteStoreEnrichesDroneMetadata asserts the storage seam derives Outcome
// from a node's lifecycle status and copies Producer from the node's Vinculum,
// so the structured columns carry real values (not just the legacy_json blob).
func TestSQLiteStoreEnrichesDroneMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(root, "history.db"), filepath.Join(root, "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// A failed probe → Drone.Outcome = FAILURE.
	probe := &Probe{
		Vinculum: Vinculum{ID: "probe_1", Kind: "probe", SessionID: "s", CollectiveID: "c", ParentID: "inc_1", CreatedAt: time.Now()},
		Name:     "bash", WireID: "c1", Status: ProbeFailed,
		Result: ResultView{Content: "boom", IsError: true},
	}
	if err := store.SaveObject(probe); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetDrone(context.Background(), probe.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != OutcomeFailure {
		t.Fatalf("probe outcome=%s want FAILURE", got.Outcome)
	}

	// A transmission whose Vinculum carries a Producer → Drone.Producer.
	tx := &Transmission{
		Vinculum: Vinculum{
			ID: "tx_1", Kind: "transmission", SessionID: "s", CollectiveID: "c", ParentID: "inc_1", CreatedAt: time.Now(),
			Producer: &Producer{Provider: "vulcan", ModelID: "qwen35-122b"},
		},
		Text: "hello",
	}
	if err := store.SaveObject(tx); err != nil {
		t.Fatal(err)
	}
	gotTx, err := store.GetDrone(context.Background(), tx.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTx.Producer.ModelID != "qwen35-122b" {
		t.Fatalf("producer=%+v", gotTx.Producer)
	}
}

func TestSQLiteSchemaHasExplicitPrimaryKeys(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "history.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, table := range []string{"interactions", "links", "summaries"} {
		rows, err := store.db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			t.Fatal(err)
		}
		hasPK := false
		for rows.Next() {
			var cid, notnull, pk int
			var name, kind string
			var def any
			if err := rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
				t.Fatal(err)
			}
			hasPK = hasPK || pk > 0
		}
		rows.Close()
		if !hasPK {
			t.Fatalf("%s has no explicit primary key", table)
		}
	}
}

// TestSQLiteStoreChatRoundTrip is the gate for flipping the live chat store from
// FileStore (JSONL) to SQLite. It builds a collective the way the chat loop does
// (CreateCollective → StartIncursion → AddTransmission → probe result), saves
// each node, then asserts ListCollectives + Resume round-trip the graph
// byte-for-byte and that re-saving is idempotent. The legacy_json + decodeByKind
// + RebuildLinks path is the only round-trip, so this catches silent data loss.
func TestSQLiteStoreChatRoundTrip(t *testing.T) {
	root := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(root, "history.db"), filepath.Join(root, "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	coll, err := store.CreateCollective(SpawnCollectiveRequest{SystemPrompt: "sys", SessionID: "sess"})
	if err != nil {
		t.Fatal(err)
	}

	inc := coll.StartIncursion("hello")
	tx1, err := inc.AddTransmission("Hi there", nil)
	if err != nil {
		t.Fatal(err)
	}
	tx2, _ := inc.AddTransmission("reading a file", []babel.ToolCall{{
		ID: "call_1", Type: "function",
		Function: babel.FunctionCall{Name: "read", Arguments: `{"file_path":"/tmp/x"}`},
	}})
	tx2.Probes[0].Status = ProbeCompleted
	tx2.Probes[0].Result = ResultView{Content: "file body", Summaries: SummarySet{Full: "file body"}}
	inc.MarkCompleted()

	// Save each node the way the chat loop does (incremental SaveObject).
	for _, obj := range []Entity{coll, inc, tx1, tx2, tx2.Probes[0]} {
		if err := store.SaveObject(obj); err != nil {
			t.Fatalf("save %s: %v", obj.ObjectKind(), err)
		}
	}

	// ListCollectives: one chat, 1 incursion, title falls back to the first prompt.
	sums, err := store.ListCollectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("expected 1 collective, got %d", len(sums))
	}
	if sums[0].ID != coll.ID {
		t.Fatalf("id=%s want %s", sums[0].ID, coll.ID)
	}
	if sums[0].Incursions != 1 {
		t.Fatalf("incursions=%d want 1", sums[0].Incursions)
	}
	if sums[0].Title != "hello" {
		t.Fatalf("title=%q want %q (first-prompt fallback)", sums[0].Title, "hello")
	}

	// Resume round-trips the full graph.
	got, err := store.Resume(coll.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemPrompt != "sys" {
		t.Fatalf("system prompt=%q", got.SystemPrompt)
	}
	if len(got.Incursions) != 1 {
		t.Fatalf("resumed incursions=%d", len(got.Incursions))
	}
	gInc := got.Incursions[0]
	if gInc.Prompt != "hello" {
		t.Fatalf("prompt=%q want hello", gInc.Prompt)
	}
	if gInc.Status != IncursionCompleted {
		t.Fatalf("status=%s want completed", gInc.Status)
	}
	if len(gInc.Transmissions) != 2 {
		t.Fatalf("transmissions=%d want 2", len(gInc.Transmissions))
	}
	if gInc.Transmissions[0].Text != "Hi there" {
		t.Fatalf("tx0=%q", gInc.Transmissions[0].Text)
	}
	if len(gInc.Transmissions[1].Probes) != 1 {
		t.Fatal("probe missing on resumed tx2")
	}
	gp := gInc.Transmissions[1].Probes[0]
	if gp.Name != "read" {
		t.Fatalf("probe name=%q", gp.Name)
	}
	if gp.WireID != "call_1" {
		t.Fatalf("probe wire id=%q", gp.WireID)
	}
	if gp.Result.Content != "file body" {
		t.Fatalf("probe result content=%q", gp.Result.Content)
	}

	// Idempotent: re-saving the whole graph doesn't duplicate rows.
	if err := store.SaveCollective(got); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM interactions WHERE id=? OR collective_id=?`, coll.ID, coll.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 { // 1 collective + 1 incursion + 2 transmissions + 1 probe
		t.Fatalf("row count after re-save=%d want 5", n)
	}
}