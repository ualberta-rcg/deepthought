package tools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Files holds read receipts for one session. os.Root confines file operations
// to the launch workspace even if a path contains a symlink or '..'.
type Files struct {
	root string
	mu   sync.Mutex
	seen map[string]string
}
type FileTool struct {
	files *Files
	name  string
}

func NewWorkspaceTools(root string) ([]Tool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if root == "/" {
		return nil, fmt.Errorf("choose a project workspace, not the filesystem root")
	}
	f := &Files{root: root, seen: map[string]string{}}
	return []Tool{&FileTool{f, "read"}, &FileTool{f, "search"}, &FileTool{f, "write"}, &FileTool{f, "edit"}}, nil
}
func (t *FileTool) Name() string   { return t.name }
func (t *FileTool) ReadOnly() bool { return t.name == "read" || t.name == "search" }
func (t *FileTool) Description() string {
	switch t.name {
	case "read":
		return "Read a workspace text file (256 KiB maximum), with line numbers and a SHA256 receipt for later edits."
	case "search":
		return "Search a literal string in workspace text files; at most 100 matches and 2000 entries. Hidden and sensitive paths are skipped."
	case "write":
		return "Atomically write a workspace file. Existing files require expected_hash from a prior read; use an empty hash to create a new file. Shows a diff for approval."
	default:
		return "Replace one unique old_text occurrence with new_text. Requires expected_hash from a prior read; rejects stale files and shows a diff for approval."
	}
}
func (t *FileTool) Parameters() map[string]any {
	str := func() any { return map[string]any{"type": "string"} }
	p := map[string]any{"file_path": str()}
	required := []string{"file_path"}
	switch t.name {
	case "read":
		p["offset"] = map[string]any{"type": "integer", "minimum": 1}
		p["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 2000}
	case "search":
		p = map[string]any{"query": map[string]any{"type": "string", "minLength": 1}, "directory": str()}
		required = []string{"query"}
	case "write":
		p["content"] = str()
		p["expected_hash"] = str()
		required = append(required, "content", "expected_hash")
	case "edit":
		p["old_text"] = map[string]any{"type": "string", "minLength": 1}
		p["new_text"] = str()
		p["expected_hash"] = str()
		required = append(required, "old_text", "new_text", "expected_hash")
	}
	return map[string]any{"type": "object", "properties": p, "required": required, "additionalProperties": false}
}

func SensitivePath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		part = strings.ToLower(part)
		if part == ".ssh" || part == ".gnupg" || part == ".git" || part == ".aws" || part == ".azure" || part == ".kube" || part == ".deepthought" || part == ".env" || strings.HasPrefix(part, ".env.") || strings.HasSuffix(part, "_history") || part == "credentials" || part == "secrets.json" || part == "host_ed25519" || strings.HasPrefix(part, "id_rsa") || strings.HasPrefix(part, "id_ed25519") || strings.HasSuffix(part, ".key") || strings.HasSuffix(part, ".pem") {
			return true
		}
	}
	return false
}

func (t *FileTool) path(path string) (string, error) {
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(t.files.root, path)
		if err != nil {
			return "", err
		}
	}
	path = filepath.Clean(path)
	if !filepath.IsLocal(path) || SensitivePath(path) {
		return "", fmt.Errorf("path is outside the workspace or contains sensitive data")
	}
	// Refuse symlinks, including aliases of sensitive files inside the workspace.
	for current := path; current != "."; current = filepath.Dir(current) {
		info, err := os.Lstat(filepath.Join(t.files.root, current))
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink paths are not supported by file tools")
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return path, nil
}

func readBounded(root *os.Root, path string) ([]byte, fs.FileMode, error) {
	f, err := root.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > readMaxBytes {
		return nil, 0, fmt.Errorf("requires a regular text file no larger than %d bytes", readMaxBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(f, readMaxBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(raw) > readMaxBytes || strings.IndexByte(string(raw), 0) >= 0 {
		return nil, 0, fmt.Errorf("file is too large or binary")
	}
	return raw, info.Mode().Perm(), nil
}
func fileHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func (t *FileTool) replacement(root *os.Root, path string, args map[string]any) ([]byte, []byte, fs.FileMode, error) {
	old, mode, err := readBounded(root, path)
	expected, _ := args["expected_hash"].(string)
	if os.IsNotExist(err) && t.name == "write" && expected == "" {
		old = nil
		mode = 0600
		err = nil
	} else if err == nil {
		if expected == "" || expected != fileHash(old) || t.files.seen[path] != expected {
			return nil, nil, 0, fmt.Errorf("read the current file first and supply its expected_hash")
		}
	}
	if err != nil {
		return nil, nil, 0, err
	}
	text, _ := args["content"].(string)
	if t.name == "edit" {
		before, _ := args["old_text"].(string)
		after, _ := args["new_text"].(string)
		if before == "" || strings.Count(string(old), before) != 1 {
			return nil, nil, 0, fmt.Errorf("old_text must match exactly once")
		}
		text = strings.Replace(string(old), before, after, 1)
	}
	if len(text) > readMaxBytes {
		return nil, nil, 0, fmt.Errorf("replacement exceeds 256 KiB")
	}
	return old, []byte(text), mode, nil
}

func (t *FileTool) Preview(ctx context.Context, args map[string]any) (string, error) {
	if t.ReadOnly() {
		return "", nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	t.files.mu.Lock()
	defer t.files.mu.Unlock()
	path, err := t.path(argString(args, "file_path"))
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(t.files.root)
	if err != nil {
		return "", err
	}
	defer root.Close()
	old, next, _, err := t.replacement(root, path, args)
	if err != nil {
		return "", err
	}
	return simpleDiff(path, string(old), string(next)), nil
}

func simpleDiff(path, old, next string) string {
	a, b := strings.Split(old, "\n"), strings.Split(next, "\n")
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	endA, endB := len(a), len(b)
	for endA > start && endB > start && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n@@ line %d @@\n", path, path, start+1)
	for _, line := range a[start:endA] {
		if out.Len() > 8192 {
			out.WriteString("[diff truncated]\n")
			break
		}
		out.WriteString("-" + line + "\n")
	}
	for _, line := range b[start:endB] {
		if out.Len() > 16384 {
			out.WriteString("[diff truncated]\n")
			break
		}
		out.WriteString("+" + line + "\n")
	}
	return out.String()
}

func (t *FileTool) Run(ctx context.Context, args map[string]any) Result {
	t.files.mu.Lock()
	defer t.files.mu.Unlock()
	failure := func(err error) Result {
		return Result{IsError: true, Content: err.Error(), Summary: t.name + " · failed"}
	}
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	root, err := os.OpenRoot(t.files.root)
	if err != nil {
		return failure(err)
	}
	defer root.Close()
	if t.name == "search" {
		return t.search(ctx, root, args)
	}
	path, err := t.path(argString(args, "file_path"))
	if err != nil {
		return failure(err)
	}
	if t.name == "read" {
		raw, _, err := readBounded(root, path)
		if err != nil {
			return failure(err)
		}
		hash := fileHash(raw)
		t.files.seen[path] = hash
		lines := strings.Split(string(raw), "\n")
		offset, limit := 0, readMaxLines
		if n, ok := numFromArgs(args["offset"]); ok {
			offset = n - 1
		}
		if n, ok := numFromArgs(args["limit"]); ok {
			limit = n
		}
		if offset < 0 {
			offset = 0
		}
		if offset > len(lines) {
			offset = len(lines)
		}
		if limit < 1 || limit > readMaxLines {
			limit = readMaxLines
		}
		var out strings.Builder
		fmt.Fprintf(&out, "SHA256: %s\n", hash)
		for i := offset; i < len(lines) && i < offset+limit; i++ {
			fmt.Fprintf(&out, "%6d→%s\n", i+1, lines[i])
		}
		return Result{Content: out.String(), Summary: "read · " + path}
	}
	old, next, mode, err := t.replacement(root, path, args)
	if err != nil {
		return failure(err)
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return failure(err)
	}
	tmp := filepath.Join(filepath.Dir(path), ".deepthought-"+hex.EncodeToString(nonce[:]))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return failure(err)
	}
	defer root.Remove(tmp)
	if _, err := f.Write(next); err != nil {
		f.Close()
		return failure(err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return failure(err)
	}
	if err := f.Close(); err != nil {
		return failure(err)
	}
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	// Recheck immediately before replacement; an external edit invalidates approval.
	current, _, readErr := readBounded(root, path)
	if (readErr != nil && !os.IsNotExist(readErr)) || (readErr == nil && fileHash(current) != fileHash(old)) || (os.IsNotExist(readErr) && argString(args, "expected_hash") != "") {
		return failure(fmt.Errorf("file changed while preparing edit; read it again"))
	}
	if err := root.Rename(tmp, path); err != nil {
		return failure(err)
	}
	delete(t.files.seen, path)
	return Result{Content: simpleDiff(path, string(old), string(next)), Summary: t.name + " · " + path}
}
func argString(args map[string]any, name string) string { s, _ := args[name].(string); return s }

func (t *FileTool) search(ctx context.Context, root *os.Root, args map[string]any) Result {
	dir := argString(args, "directory")
	if dir == "" {
		dir = "."
	}
	dir, err := t.path(dir)
	if err != nil {
		return Result{IsError: true, Content: err.Error(), Summary: "search · refused"}
	}
	query := argString(args, "query")
	if query == "" {
		return Result{IsError: true, Content: "query required", Summary: "search · invalid"}
	}
	var out strings.Builder
	entries, matches := 0, 0
	err = fs.WalkDir(root.FS(), filepath.ToSlash(dir), func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		entries++
		if entries > 2000 || matches >= 100 || out.Len() > readMaxBytes {
			return fs.SkipAll
		}
		if SensitivePath(path) || strings.HasPrefix(d.Name(), ".") && path != "." || d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		raw, _, err := readBounded(root, path)
		if err != nil {
			return nil
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, query) {
				fmt.Fprintf(&out, "%s:%d:%s\n", path, i+1, line)
				matches++
				if matches >= 100 {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return Result{IsError: true, Content: err.Error(), Summary: "search · failed"}
	}
	return Result{Content: out.String(), Summary: fmt.Sprintf("search · %d matches (bounded)", matches)}
}
