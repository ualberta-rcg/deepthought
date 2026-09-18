package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readMaxBytes is the whole-file size cap (pre-read, via stat). Mirrors the
// reference's 256 KB MAX_OUTPUT_SIZE: a file larger than this is refused outright
// rather than partially read, nudging the model to use offset/limit or another tool.
const readMaxBytes = 256 << 10

// readMaxLines caps the number of lines returned when the model doesn't pass a limit.
const readMaxLines = 2000

// binaryExts are extensions we refuse to read as text. Binary-ness is decided by
// extension, not content sniffing (same as the reference) — cheap and predictable.
var binaryExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true,
	".ico": true, ".tiff": true,
	".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".tar": true,
	".7z": true, ".rar": true,
	".pdf": true,
	".so":  true, ".o": true, ".a": true, ".dylib": true, ".dll": true, ".exe": true,
	".class": true, ".jar": true,
	".mp3": true, ".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".wav": true,
	".dat": true,
}

// Read reads a text file, returning cat -n numbered lines. It is read-only (Queen
// auto-allows it). Supports offset (1-indexed) and limit. Refuses relative paths,
// binary extensions, files past the size cap, and unreadable paths.
type Read struct{}

// NewRead returns the read tool.
func NewRead() Read { return Read{} }

func (Read) Name() string { return "read" }

func (Read) Description() string {
	return "Read a text file from the local filesystem, returning cat -n numbered " +
		"lines. Use absolute paths. Supports offset (1-indexed line to start at) and " +
		"limit (number of lines). Refuses binary files and files over 256 KB."
}

func (Read) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path of the file to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "1-indexed line number to start reading from.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Number of lines to read.",
			},
		},
		"required":             []string{"file_path"},
		"additionalProperties": false,
	}
}

// ReadOnly is true: read never mutates, so Queen auto-allows it.
func (Read) ReadOnly() bool { return true }

// Run reads the file. Errors are returned as Result.IsError with a clear message
// rather than a Go error, so the model gets actionable feedback.
func (Read) Run(_ context.Context, args map[string]any) Result {
	path, _ := args["file_path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return Result{IsError: true, Content: "read: missing 'file_path' argument", Summary: "read · missing path"}
	}
	if !filepath.IsAbs(path) {
		return Result{IsError: true, Content: "read: file_path must be absolute, got " + path, Summary: "read · relative path"}
	}
	if binaryExts[strings.ToLower(filepath.Ext(path))] {
		return Result{IsError: true, Content: "read: refusing binary file " + path, Summary: "read · binary"}
	}

	info, err := os.Stat(path)
	if err != nil {
		return Result{IsError: true, Content: "read: " + cleanStatErr(err), Summary: "read · stat failed"}
	}
	if info.Size() > readMaxBytes {
		return Result{IsError: true,
			Content: fmt.Sprintf("read: %s is %d bytes (cap %d); use offset/limit or another tool", path, info.Size(), readMaxBytes),
			Summary: "read · too large",
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{IsError: true, Content: "read: " + cleanStatErr(err), Summary: "read · failed"}
	}
	raw = stripBOM(raw)

	// Normalize line endings and drop a single trailing newline so a file that ends
	// in "\n" doesn't produce a spurious empty last line (the conventional case).
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if strings.HasSuffix(text, "\n") {
		text = text[:len(text)-1]
	}

	// Offset is 1-indexed in the schema → 0-indexed slice start.
	offset := 0
	if o, ok := numFromArgs(args["offset"]); ok && o > 0 {
		offset = o - 1
	}
	limit := readMaxLines
	if l, ok := numFromArgs(args["limit"]); ok && l > 0 {
		limit = l
	}

	lines := strings.Split(text, "\n")
	if offset > len(lines) {
		offset = len(lines)
	}
	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}
	slice := lines[offset:end]

	var b strings.Builder
	for i, ln := range slice {
		fmt.Fprintf(&b, "%6d→%s\n", offset+i+1, ln)
	}
	body := strings.TrimRight(b.String(), "\n")
	n := len(slice)
	if n == 0 {
		body = "(empty)"
	}
	return Result{
		Content: body,
		Summary: fmt.Sprintf("read %s · %d line%s", shortPath(path), n, plural(n)),
	}
}

func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

// shortPath elides the home prefix for terser transcript chrome.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// cleanStatErr trims the "open /path: " prefix os errors carry, since we already
// name the tool and path in our own framing.
func cleanStatErr(err error) string {
	return strings.TrimPrefix(err.Error(), "open ")
}
