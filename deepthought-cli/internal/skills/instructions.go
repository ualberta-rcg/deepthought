package skills

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ProjectInstructions loads repository-root to working-directory guidance.
// It never walks siblings or user directories outside the discovered project.
func ProjectInstructions(cwd string) (string, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	root := locateProject(cwd)
	var dirs []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == root {
			break
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("invalid instruction root")
		}
	}
	var out strings.Builder
	for i := len(dirs) - 1; i >= 0; i-- {
		for _, name := range []string{"AGENTS.md", "CLAUDE.md", "DEEPTHOUGHT_CLI.md"} {
			path := filepath.Join(dirs[i], name)
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				return "", err
			}
			raw, err := io.ReadAll(io.LimitReader(f, 32769))
			f.Close()
			if err != nil {
				return "", err
			}
			if len(raw) > 32768 || out.Len()+len(raw) > 65536 {
				return "", fmt.Errorf("project instructions exceed 64 KiB; shorten %s", path)
			}
			fmt.Fprintf(&out, "\n[Project instructions from %s; deeper directory instructions take precedence within their scope.]\n%s\n", path, raw)
		}
	}
	return out.String(), nil
}
