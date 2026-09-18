package tools

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GuardCommand rejects filesystem operations that are unsafe on a shared
// parallel filesystem. It intentionally targets only broad, unmistakable
// patterns; Queen remains responsible for destructive-command policy.
func GuardCommand(command string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(command)), " ")
	dangerous := []string{
		"find / ",
		"find /;",
		"find /|",
		"find / -",
		"ls -r /",
		"ls -lr /",
		"du -a /",
		"du --all /",
		":(){ :|:& };:", // common fork bomb spelling after whitespace folding
	}
	for _, pattern := range dangerous {
		if strings.Contains(normalized, pattern) {
			return "unbounded recursive operation on the filesystem root; target a specific directory and add a depth or result bound"
		}
	}
	return ""
}

// StorageTier describes a filesystem's durability and operational constraints.
type StorageTier struct {
	Name         string
	Root         string
	BackedUp     bool
	PurgeHorizon time.Duration
}

// ExpiresAt returns the expected purge boundary for a file last made active at
// t. A zero result means the tier has no automatic purge.
func (s StorageTier) ExpiresAt(t time.Time) time.Time {
	if s.PurgeHorizon <= 0 {
		return time.Time{}
	}
	return t.Add(s.PurgeHorizon)
}

// StorageTierFor classifies a path using the current user's site environment.
// Unknown paths are deliberately marked unknown rather than assumed durable.
func StorageTierFor(path string) StorageTier {
	clean := filepath.Clean(path)
	home, _ := os.UserHomeDir()
	scratch := os.Getenv("SCRATCH")
	project := os.Getenv("PROJECT")
	switch {
	case within(clean, scratch):
		return StorageTier{Name: "scratch", Root: scratch, PurgeHorizon: 60 * 24 * time.Hour}
	case within(clean, project):
		return StorageTier{Name: "project", Root: project, BackedUp: true}
	case within(clean, home):
		return StorageTier{Name: "home", Root: home, BackedUp: true}
	default:
		return StorageTier{Name: "unknown"}
	}
}

func within(path, root string) bool {
	if root == "" {
		return false
	}
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
