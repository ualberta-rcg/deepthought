// Package server holds the deepthought-server runtime helpers. This module
// depends on the CLI in NO way — the Borg-graph wire shapes live in the
// local graph package (a deliberate, documented duplication of the client's
// history types; change both together).
package server

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureDirs creates the server's data layout under dir.
func EnsureDirs(dir string) error {
	for _, d := range []string{dir, filepath.Join(dir, "cron")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("server: %w", err)
		}
	}
	return nil
}
