package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceEditReceiptAndStaleness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("before\n"), 0600)
	ts, err := NewWorkspaceTools(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(ts...)
	read, _ := r.Lookup("read")
	edit, _ := r.Lookup("edit")
	args := map[string]any{"file_path": "a.txt", "old_text": "before", "new_text": "after", "expected_hash": fileHash([]byte("before\n"))}
	if result := edit.Run(context.Background(), args); !result.IsError {
		t.Fatal("edit accepted without prior read")
	}
	if result := read.Run(context.Background(), map[string]any{"file_path": "a.txt"}); result.IsError {
		t.Fatal(result.Content)
	}
	preview, err := edit.(*FileTool).Preview(context.Background(), args)
	if err != nil || !strings.Contains(preview, "+after") {
		t.Fatalf("preview %q %v", preview, err)
	}
	os.WriteFile(path, []byte("changed externally\n"), 0600)
	if result := edit.Run(context.Background(), args); !result.IsError {
		t.Fatal("stale edit accepted")
	}
	read.Run(context.Background(), map[string]any{"file_path": "a.txt"})
	args["old_text"] = "changed externally"
	args["expected_hash"] = fileHash([]byte("changed externally\n"))
	if result := edit.Run(context.Background(), args); result.IsError {
		t.Fatal(result.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "after\n" {
		t.Fatalf("written %q", got)
	}
}

func TestWorkspaceEscapeAndSensitiveFiles(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(dir, "link"))
	os.WriteFile(filepath.Join(dir, ".env"), []byte("fixture only"), 0600)
	ts, _ := NewWorkspaceTools(dir)
	read := ts[0]
	for _, path := range []string{outside, "../outside", "link", ".env"} {
		if result := read.Run(context.Background(), map[string]any{"file_path": path}); !result.IsError {
			t.Fatalf("read forbidden path %s", path)
		}
	}
}

func TestWorkspaceForkDoesNotShareReadReceipts(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a"), []byte("a"), 0600)
	ts, _ := NewWorkspaceTools(dir)
	first := NewRegistry(ts...)
	second := first.Fork()
	read, _ := first.Lookup("read")
	read.Run(context.Background(), map[string]any{"file_path": "a"})
	write, _ := second.Lookup("write")
	result := write.Run(context.Background(), map[string]any{"file_path": "a", "content": "b", "expected_hash": fileHash([]byte("a"))})
	if !result.IsError {
		t.Fatal("read receipt leaked across sessions")
	}
}
