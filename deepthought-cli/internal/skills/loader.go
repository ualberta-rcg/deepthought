// Package skills loads versioned skill packs with progressive disclosure.
package skills

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"deepthought-cli/internal/config"
)

const PackFormatVersion = 1

type Frontmatter struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	WhenToUse    string   `yaml:"when_to_use"`
	AllowedTools []string `yaml:"allowed-tools"`
	Version      any      `yaml:"version"`
}

type Skill struct {
	Frontmatter
	Path  string
	Layer string
	body  []byte
}

func (s *Skill) Body() (string, error) {
	if s.body == nil {
		raw, err := os.ReadFile(s.Path)
		if err != nil {
			return "", err
		}
		_, body, err := splitFrontmatter(raw)
		if err != nil {
			return "", err
		}
		s.body = body
	}
	return string(s.body), nil
}

// Script returns a real path inside the skill directory without loading script
// contents into model context.
func (s *Skill) Script(relative string) (string, error) {
	root, err := filepath.EvalSymlinks(filepath.Dir(s.Path))
	if err != nil {
		return "", err
	}
	candidate, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("skills: script escapes pack")
	}
	return candidate, nil
}

type Loader struct {
	SiteRoots   []string // caller-supplied, lowest precedence
	SystemRoots []string // org-managed locations, second-lowest
	UserHome    string
	BaseDir     string
	mu          sync.Mutex
	cache       map[string][]*Skill
}

// DefaultSystemRoots are the org-managed skill locations scanned at the lowest
// precedence — anything personal or project can shadow them by name. Nonexistent
// roots are skipped by Load. This is where cluster-wide skills such as the
// alliance-* packs live on a managed node.
func DefaultSystemRoots() []string {
	return []string{
		"/etc/claude-code/.claude/skills", // org-managed Claude Code skills
		"/etc/deepthought-cli/skills",     // org-managed DeepThought skills
	}
}

func NewLoader(siteRoots ...string) *Loader {
	home, _ := os.UserHomeDir()
	base := home
	if b, err := config.BaseDir(); err == nil {
		base = b
	}
	return &Loader{
		SiteRoots:   siteRoots,
		SystemRoots: DefaultSystemRoots(),
		UserHome:    home,
		BaseDir:     base,
		cache:       map[string][]*Skill{},
	}
}

// Load resolves skills across every search root and keeps the first one seen for
// each name. Precedence (highest wins): user > user-claude > user-codex >
// project > project-claude > project-codex > system > site. A personal or
// project skill therefore shadows a same-named org-managed one.
func (l *Loader) Load(cwd string) ([]*Skill, error) {
	realCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		realCWD = filepath.Clean(cwd)
	}
	l.mu.Lock()
	if cached, ok := l.cache[realCWD]; ok {
		out := append([]*Skill(nil), cached...)
		l.mu.Unlock()
		return out, nil
	}
	l.mu.Unlock()
	projectRoot := locateProject(realCWD)
	roots := []struct {
		path, layer string
	}{
		{filepath.Join(l.BaseDir, "skills"), "user"},
		{filepath.Join(l.UserHome, ".claude", "skills"), "user-claude"},
		{filepath.Join(l.UserHome, ".codex", "skills"), "user-codex"},
		{filepath.Join(projectRoot, ".deepthought-cli", "skills"), "project"},
		{filepath.Join(projectRoot, ".claude", "skills"), "project-claude"},
		{filepath.Join(projectRoot, ".codex", "skills"), "project-codex"},
	}
	for _, root := range l.SystemRoots {
		roots = append(roots, struct{ path, layer string }{root, "system"})
	}
	for _, root := range l.SiteRoots {
		roots = append(roots, struct{ path, layer string }{root, "site"})
	}
	seenName, seenPath := map[string]bool{}, map[string]bool{}
	var out []*Skill
	for _, root := range roots {
		entries, err := os.ReadDir(root.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			path := filepath.Join(root.path, entry.Name())
			if entry.IsDir() {
				path = filepath.Join(path, "SKILL.md")
			}
			if filepath.Base(path) != "SKILL.md" {
				continue
			}
			real, err := filepath.EvalSymlinks(path)
			if err != nil || seenPath[real] {
				continue
			}
			skill, err := readDescriptor(real, root.layer)
			if err != nil {
				return nil, err
			}
			seenPath[real] = true
			if seenName[skill.Name] {
				continue
			}
			seenName[skill.Name] = true
			out = append(out, skill)
		}
	}
	l.mu.Lock()
	l.cache[realCWD] = append([]*Skill(nil), out...)
	l.mu.Unlock()
	return out, nil
}

func locateProject(cwd string) string {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "DEEPTHOUGHT_CLI.md")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, ".deepthought-cli")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
	}
}

func readDescriptor(path, layer string) (*Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	header, _, err := splitFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("skills: %s: %w", path, err)
	}
	var metadata Frontmatter
	if err := yaml.Unmarshal(header, &metadata); err != nil {
		return nil, fmt.Errorf("skills: %s: %w", path, err)
	}
	if metadata.Name == "" || metadata.Description == "" {
		return nil, fmt.Errorf("skills: %s: name and description are required", path)
	}
	return &Skill{Frontmatter: metadata, Path: path, Layer: layer}, nil
}

func splitFrontmatter(raw []byte) ([]byte, []byte, error) {
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return nil, nil, fmt.Errorf("missing YAML frontmatter")
	}
	rest := raw[4:]
	at := bytes.Index(rest, []byte("\n---\n"))
	if at < 0 {
		return nil, nil, fmt.Errorf("unterminated YAML frontmatter")
	}
	return rest[:at], rest[at+5:], nil
}
