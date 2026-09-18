package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProvenanceStalenessAcceptance(t *testing.T) {
	directive := NewDirective("session", "two-objective experiment")
	first := directive.AddObjective("generate intermediate", Predicate{Kind: PredicateExitZero, ExitCode: 0})
	second := directive.AddObjective("consume intermediate", Predicate{Kind: PredicateFileExists})
	second.Dependencies = []string{first.ID}
	second.LinkTo(first, "depends_on")

	first.Status = StatusReady
	attempt := first.StartAttempt("v1-test")
	if attempt.ObjectiveID != first.ID || attempt.Environment.CapturedAt.IsZero() {
		t.Fatal("attempt lacks objective link or captured environment")
	}

	a := &Artifact{Vinculum: Vinculum{ID: "a"}, Hash: "parameter=1", ConsumedHashes: map[string]string{}}
	b := &Artifact{Vinculum: Vinculum{ID: "b"}, Hash: "intermediate-v1", ConsumedHashes: map[string]string{"a": "parameter=1"}}
	c := &Artifact{Vinculum: Vinculum{ID: "c"}, Hash: "result-v1", ConsumedHashes: map[string]string{"b": "intermediate-v1"}}
	first.Outputs = []string{b.ID}
	second.Inputs, second.Outputs = []string{b.ID}, []string{c.ID}
	graph := map[string]*Artifact{"a": a, "b": b, "c": c}

	// Re-run with one parameter changed.
	a.Hash = "parameter=2"
	stale := StaleArtifacts(graph, nil)
	got := map[string]bool{}
	for _, id := range stale {
		got[id] = true
	}
	if !got["b"] || !got["c"] || got["a"] {
		t.Fatalf("downstream stale set = %v", stale)
	}
}

func TestPredicatesAndLazyDecomposition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.txt")
	if err := os.WriteFile(path, []byte("loss=0.1"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := (Predicate{Kind: PredicateRegexIn, Path: path, Pattern: `loss=0\.[0-9]+`}).Evaluate(context.Background())
	if err != nil || !ok {
		t.Fatalf("predicate ok=%v err=%v", ok, err)
	}
	objective := &Objective{Vinculum: Vinculum{ID: "o", SessionID: "s"}, Status: StatusPending}
	if _, err := objective.Decompose("too early"); err == nil {
		t.Fatal("eager decomposition was allowed")
	}
	objective.Status = StatusReady
	children, err := objective.Decompose("one", "two")
	if err != nil || len(children) != 2 {
		t.Fatalf("children=%v err=%v", children, err)
	}
}

func TestModelJudgeIsUnverified(t *testing.T) {
	if ok, err := (Predicate{Kind: PredicateModelJudge}).Evaluate(context.Background()); err == nil || ok {
		t.Fatal("model judge should require sign-off")
	}
}
