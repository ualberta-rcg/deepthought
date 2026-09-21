package slurm

import (
	"context"
	"deepthought-cli/internal/history"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type submissionRunner struct {
	calls     int
	ambiguous bool
	visible   bool
}

func (r *submissionRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "sacctmgr":
		return []byte("def-test|\n"), nil
	case "sbatch":
		r.calls++
		if r.ambiguous {
			return nil, fmt.Errorf("response lost")
		}
		return []byte("123"), nil
	case "squeue", "sacct":
		if r.visible {
			return []byte(`{"jobs":[{"job_id":123,"name":"deepthought-test","comment":"deepthought:test","job_state":"COMPLETED"}]}`), nil
		}
		return []byte(`{"jobs":[]}`), nil
	}
	return nil, fmt.Errorf("unexpected command %s", name)
}
func TestSubmissionJournalPreventsDuplicateAfterLostResponse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SCRATCH", dir)
	t.Setenv("USER", "fixture")
	script := filepath.Join(dir, "input.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\ntrue\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := history.NewSQLiteStore(filepath.Join(dir, "history.db"), filepath.Join(dir, "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := &submissionRunner{ambiguous: true}
	c := NewClient(runner)
	c.Store = store
	r := SubmitRequest{SubmissionID: "test", Script: script, CPUs: 1, Account: "def-test", Time: "00:05:00", Memory: "1G"}
	if _, err := c.Submit(context.Background(), r); err == nil {
		t.Fatal("lost response appeared successful")
	}
	if _, err := c.Submit(context.Background(), r); err == nil {
		t.Fatal("unresolved submission retried")
	}
	if runner.calls != 1 {
		t.Fatalf("sbatch called %d times", runner.calls)
	}
	runner.visible = true
	id, err := c.Submit(context.Background(), r)
	if err != nil || id != "123" {
		t.Fatalf("reconcile id=%s error=%v", id, err)
	}
	// A new process can recover the same intent without executing sbatch again.
	other := NewClient(runner)
	other.Store = store
	if _, err := other.Submit(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	r.Memory = "2G"
	if _, err := c.Submit(context.Background(), r); err == nil {
		t.Fatal("changed resources reused an intent")
	}
	if runner.calls != 1 {
		t.Fatal("duplicate sbatch")
	}
	staged, err := os.ReadFile(filepath.Join(dir, "deepthought-cli", "jobs", "test", "script.sh"))
	if err != nil || string(staged) != "#!/bin/bash\ntrue\n" {
		t.Fatal("script was not staged")
	}
}
