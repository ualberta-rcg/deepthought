package history

import (
	"path/filepath"
	"testing"

	"deepthought-cli/internal/babel"
)

// toolContent extracts the tool-result message body from a flattened slice.
func toolContent(msgs []babel.Message) string {
	for _, m := range msgs {
		if m.Role == "tool" {
			return m.Content
		}
	}
	return ""
}

// TestDemoteProbeShortensContext is the Phase-4 seam test: demoting a probe to
// tombstone writes the marker, persists the state, and — after a reload — makes
// MessagesByState render the tombstone instead of the full body. Nothing demotes
// automatically in the chat loop; this is the path a future Queen thread drives.
func TestDemoteProbeShortensContext(t *testing.T) {
	root := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(root, "h.db"), filepath.Join(root, "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	coll, err := store.CreateCollective(SpawnCollectiveRequest{SystemPrompt: "sys", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	inc := coll.StartIncursion("hello")
	tx, _ := inc.AddTransmission("reading", []babel.ToolCall{{
		ID: "c1", Type: "function", Function: babel.FunctionCall{Name: "read", Arguments: `{}`},
	}})
	tx.Probes[0].Result = ResultView{Content: "big body", Summaries: SummarySet{Full: "big body"}}
	inc.MarkCompleted()
	probeID := tx.Probes[0].ID
	for _, obj := range []Entity{coll, inc, tx, tx.Probes[0]} {
		if err := store.SaveObject(obj); err != nil {
			t.Fatal(err)
		}
	}

	// Sanity: before demotion the tool result renders full.
	if got := toolContent(coll.MessagesByState()); got != "big body" {
		t.Fatalf("pre-demotion tool content=%q want big body", got)
	}

	// Demote to tombstone (the Queen-thread seam).
	if err := DemoteProbe(store, coll.ID, probeID, StateTombstone); err != nil {
		t.Fatal(err)
	}

	// Reload: the state column is rehydrated onto the probe, and MessagesByState
	// now renders the tombstone marker.
	reloaded, err := store.GetCollective(coll.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := reloaded.Incursions[0].Transmissions[0].Probes[0]
	if p.State != StateTombstone {
		t.Fatalf("rehydrated state=%s want TOMBSTONE", p.State)
	}
	if got := toolContent(reloaded.MessagesByState()); got != "[summarized]" {
		t.Fatalf("post-demotion tool content=%q want [summarized]", got)
	}
}

// TestDemoteProbeRefusesPromotion asserts the seam is monotonic: you can't
// "demote" upward.
func TestDemoteProbeRefusesPromotion(t *testing.T) {
	root := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(root, "h.db"), filepath.Join(root, "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	coll, _ := store.CreateCollective(SpawnCollectiveRequest{SystemPrompt: "sys", SessionID: "s"})
	inc := coll.StartIncursion("hi")
	tx, _ := inc.AddTransmission("r", []babel.ToolCall{{
		ID: "c1", Type: "function", Function: babel.FunctionCall{Name: "read", Arguments: `{}`},
	}})
	tx.Probes[0].Result = ResultView{Content: "x", Summaries: SummarySet{Full: "x"}}
	probeID := tx.Probes[0].ID
	for _, obj := range []Entity{coll, inc, tx, tx.Probes[0]} {
		_ = store.SaveObject(obj)
	}
	// Tombstone first (succeeds), then try to "demote" back to Full.
	if err := DemoteProbe(store, coll.ID, probeID, StateTombstone); err != nil {
		t.Fatal(err)
	}
	if err := DemoteProbe(store, coll.ID, probeID, StateFull); err == nil {
		t.Fatal("expected promotion to be refused")
	}
}
