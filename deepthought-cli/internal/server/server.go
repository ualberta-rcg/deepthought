// Package server holds the shared helpers of cmd/deepthought-server. Kept
// internal so the eventual promotion to the deepthought-server product dir is
// a move, not an import-boundary break.
package server

import (
	"fmt"
	"os"
	"path/filepath"

	"deepthought-cli/internal/cron"
)

// LoadCronRegistry reads the local cron tracking registry (empty when absent).
func LoadCronRegistry(dataDir string) (cron.Registry, error) {
	return cron.LoadRegistry(filepath.Join(dataDir, "cron"))
}

// EnsureDirs creates the server's data layout under dir.
func EnsureDirs(dir string) error {
	for _, d := range []string{dir, filepath.Join(dir, "cron")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("server: %w", err)
		}
	}
	return nil
}
