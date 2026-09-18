package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- registry ---

func TestRegistryLookupAndSchemas(t *testing.T) {
	r := NewRegistry(NewBash(), NewRead())
	b, ok := r.Lookup("bash")
	if !ok || b.Name() != "bash" {
		t.Fatal("Lookup(bash) failed")
	}
	if _, ok := r.Lookup("nope"); ok {
		t.Fatal("Lookup(nope) should be false")
	}
	sch := r.Schemas()
	if len(sch) != 2 {
		t.Fatalf("Schemas len = %d, want 2", len(sch))
	}
	if sch[0].Type != "function" || sch[0].Function.Name == "" {
		t.Errorf("Schemas[0] malformed: %+v", sch[0])
	}
	// Schemas must be stable across calls (cache-friendly).
	again := r.Schemas()
	if sch[0].Function.Name != again[0].Function.Name {
		t.Error("Schemas order is not stable")
	}
}

// --- bash ---

func TestBashSuccess(t *testing.T) {
	res := NewBash().Run(context.Background(), map[string]any{"command": "echo hello"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "hello") {
		t.Errorf("Content = %q", res.Content)
	}
	if res.Summary != "bash → exit 0" {
		t.Errorf("Summary = %q", res.Summary)
	}
}

func TestBashExitCode(t *testing.T) {
	res := NewBash().Run(context.Background(), map[string]any{"command": "exit 7"})
	if !res.IsError {
		t.Fatal("want IsError for non-zero exit")
	}
	if !strings.Contains(res.Content, "Exit code 7") {
		t.Errorf("Content = %q, want Exit code 7", res.Content)
	}
	if res.Summary != "bash → exit 7" {
		t.Errorf("Summary = %q", res.Summary)
	}
}

func TestBashMissingCommand(t *testing.T) {
	res := NewBash().Run(context.Background(), map[string]any{})
	if !res.IsError {
		t.Fatal("want IsError for missing command")
	}
}

func TestBashTimeout(t *testing.T) {
	res := NewBash().Run(context.Background(), map[string]any{
		"command":    "sleep 5",
		"timeout_ms": 200,
	})
	if !res.IsError {
		t.Fatal("want IsError on timeout")
	}
	if !strings.Contains(res.Content, "timed out") {
		t.Errorf("Content = %q, want timed out", res.Content)
	}
}

func TestBashPersistsState(t *testing.T) {
	b := NewBash()
	dir := t.TempDir()
	first := b.Run(context.Background(), map[string]any{
		"command": "cd " + dir + " && export DEEPTHOUGHT_CLI_PERSIST=yes",
	})
	if first.IsError {
		t.Fatal(first.Content)
	}
	second := b.Run(context.Background(), map[string]any{
		"command": `printf '%s|%s' "$PWD" "$DEEPTHOUGHT_CLI_PERSIST"`,
	})
	if second.IsError || second.Content != dir+"|yes" {
		t.Fatalf("persistent state = %+v", second)
	}
}

func TestBashRefusesRootWalk(t *testing.T) {
	res := NewBash().Run(context.Background(), map[string]any{"command": "find / -type f"})
	if !res.IsError || !strings.Contains(res.Content, "unbounded recursive") {
		t.Fatalf("want filesystem guard, got %+v", res)
	}
}

func TestStorageTierFor(t *testing.T) {
	t.Setenv("SCRATCH", "/scratch/tester")
	t.Setenv("PROJECT", "/project/tester")
	if got := StorageTierFor("/scratch/tester/run/out"); got.Name != "scratch" || got.PurgeHorizon == 0 {
		t.Fatalf("scratch tier = %+v", got)
	}
	if got := StorageTierFor("/project/tester/data"); got.Name != "project" || !got.BackedUp {
		t.Fatalf("project tier = %+v", got)
	}
}

// --- read ---

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadLineNumbers(t *testing.T) {
	p := writeFile(t, "f.txt", "alpha\nbeta\ngamma\n")
	res := NewRead().Run(context.Background(), map[string]any{"file_path": p})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "1→alpha") || !strings.Contains(res.Content, "2→beta") {
		t.Errorf("Content missing cat -n numbering:\n%s", res.Content)
	}
	if !strings.Contains(res.Summary, "3 line") {
		t.Errorf("Summary = %q", res.Summary)
	}
}

func TestReadOffsetLimit(t *testing.T) {
	p := writeFile(t, "f.txt", "l1\nl2\nl3\nl4\nl5\n")
	res := NewRead().Run(context.Background(), map[string]any{"file_path": p, "offset": 2, "limit": 2})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "2→l2") || !strings.Contains(res.Content, "3→l3") {
		t.Errorf("Content = %q", res.Content)
	}
	if strings.Contains(res.Content, "l1") || strings.Contains(res.Content, "l4") {
		t.Errorf("offset/limit not respected: %s", res.Content)
	}
}

func TestReadRejectsRelativePath(t *testing.T) {
	res := NewRead().Run(context.Background(), map[string]any{"file_path": "relative/path.txt"})
	if !res.IsError || !strings.Contains(res.Content, "absolute") {
		t.Fatalf("want relative-path error, got: %+v", res)
	}
}

func TestReadRejectsBinary(t *testing.T) {
	p := writeFile(t, "f.png", "not really png")
	res := NewRead().Run(context.Background(), map[string]any{"file_path": p})
	if !res.IsError || !strings.Contains(res.Content, "binary") {
		t.Fatalf("want binary error, got: %+v", res)
	}
}

func TestReadRejectsMissingFile(t *testing.T) {
	res := NewRead().Run(context.Background(), map[string]any{"file_path": "/definitely/does/not/exist.xyz"})
	if !res.IsError {
		t.Fatal("want error for missing file")
	}
}

func TestReadRejectsLargeFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	big := strings.Repeat("x", readMaxBytes+1)
	if err := os.WriteFile(p, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	res := NewRead().Run(context.Background(), map[string]any{"file_path": p})
	if !res.IsError || res.Summary != "read · too large" {
		t.Fatalf("want size-cap error, got: %+v", res)
	}
}
