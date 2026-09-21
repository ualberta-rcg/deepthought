package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// OwnedPath limits scheduler inputs and result reads to the caller's regular,
// non-sensitive files. Resolve links before checking the final path and owner.
func OwnedPath(path string) (string, error) {
	if !filepath.IsAbs(path) || SensitivePath(path) {
		return "", fmt.Errorf("an absolute non-sensitive path is required")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if SensitivePath(real) {
		return "", fmt.Errorf("sensitive path")
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(st.Uid) != os.Getuid() {
		return "", fmt.Errorf("file must be regular and owned by the current user")
	}
	return real, nil
}
