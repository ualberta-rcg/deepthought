package workflow

import (
	"context"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/slurm"
	"os"
	"path/filepath"
	"testing"
)

func TestTwoObjectiveWorkflowReopenAndStaleness(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "history.db")
	store, err := history.NewSQLiteStore(db, filepath.Join(dir, "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Version: "fixture"}
	first, second := filepath.Join(dir, "intermediate"), filepath.Join(dir, "result")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("42"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.Create("two stages", map[string]any{"seed": 1}, []Step{{"prepare", history.Predicate{Kind: history.PredicateFileExists, Path: first}}, {"analyze", history.Predicate{Kind: history.PredicateRegexIn, Path: second, Pattern: "^42$"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := p.Directive.ID
	one, two := p.Directive.Objectives[0].ID, p.Directive.Objectives[1].ID
	if _, err := s.Validate(context.Background(), id, two, second, "scratch"); err == nil {
		t.Fatal("dependency bypass")
	}
	if err := store.PutRecord("submission", "approved", slurm.Submission{ID: "approved", JobID: "123", ScriptHash: "script-hash"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Link(id, one, "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Validate(context.Background(), id, one, first, "scratch"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = history.NewSQLiteStore(db, filepath.Join(dir, "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.Store = store
	p, err = s.Validate(context.Background(), id, two, second, "scratch")
	if err != nil {
		t.Fatal(err)
	}
	if p.Directive.Status != history.StatusSucceeded || len(p.Artifacts) != 2 || len(p.Directive.Objectives[0].Attempts) != 1 {
		t.Fatalf("incomplete workflow: %+v", p)
	}
	before := *p
	p, err = s.Reparameter(id, map[string]any{"seed": 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Artifacts {
		if !a.Stale {
			t.Fatal("artifact remained valid after parameter change")
		}
	}
	if err := s.save(&before); err == nil {
		t.Fatal("concurrent update overwrote newer parameters")
	}
}
