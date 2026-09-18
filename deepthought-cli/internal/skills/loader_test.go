package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, root, dir, name, description string) string {
	t.Helper()
	path := filepath.Join(root, dir)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "SKILL.md")
	raw := "---\nname: " + name + "\ndescription: " + description + "\nwhen_to_use: test\nallowed-tools:\n  - Bash(squeue *)\nversion: 1\n---\n\n# Body\nsecret details\n"
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestLayeringAndProgressiveDisclosure(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	site := filepath.Join(root, "site")
	writeSkill(t, site, "slurm", "slurm", "site")
	writeSkill(t, filepath.Join(project, ".deepthought-cli", "skills"), "slurm", "slurm", "project")
	writeSkill(t, filepath.Join(home, ".deepthought", "skills"), "slurm", "slurm", "user")
	if err := os.MkdirAll(filepath.Join(project, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(site)
	loader.SystemRoots = nil // keep this test hermetic from the host's org skills
	loader.UserHome = home
	loader.BaseDir = filepath.Join(home, ".deepthought")
	skills, err := loader.Load(filepath.Join(project, "subdir"))
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Description != "user" || skills[0].body != nil {
		t.Fatalf("skills=%+v", skills)
	}
	body, err := skills[0].Body()
	if err != nil || body == "" || skills[0].body == nil {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestClaudeSkillFrontmatterLoadsUnmodified(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "alliance-slurm", "alliance-slurm", "Alliance Slurm")
	loader := NewLoader()
	loader.SystemRoots = nil // keep this test hermetic from the host's org skills
	loader.UserHome = home
	skills, err := loader.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "alliance-slurm" || len(skills[0].AllowedTools) != 1 {
		t.Fatalf("skills=%+v", skills)
	}
}

func TestCodexClaudeAndSystemRoots(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	sys := filepath.Join(root, "sys")

	// "gpu" in personal codex AND in system: codex (higher) must win.
	writeSkill(t, filepath.Join(home, ".codex", "skills"), "gpu", "gpu", "codex-gpu")
	writeSkill(t, sys, "gpu", "gpu", "sys-gpu")
	// system-only skill.
	writeSkill(t, sys, "net", "net", "sys-net")
	// Claude *project* skill (a location the loader used to miss).
	writeSkill(t, filepath.Join(project, ".claude", "skills"), "proj", "proj", "proj-claude")

	loader := NewLoader()
	loader.UserHome = home
	loader.BaseDir = filepath.Join(home, ".deepthought")
	loader.SystemRoots = []string{sys} // hermetic stand-in for /etc/...

	got, err := loader.Load(project)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*Skill{}
	for _, s := range got {
		byName[s.Name] = s
	}
	if len(got) != 3 {
		t.Fatalf("want 3 skills (gpu, net, proj), got %d: %+v", len(got), got)
	}
	if byName["gpu"].Description != "codex-gpu" || byName["gpu"].Layer != "user-codex" {
		t.Errorf("gpu should resolve to personal codex: desc=%q layer=%q", byName["gpu"].Description, byName["gpu"].Layer)
	}
	if byName["net"].Layer != "system" {
		t.Errorf("net should be system layer: %q", byName["net"].Layer)
	}
	if byName["proj"].Layer != "project-claude" {
		t.Errorf("proj should be project-claude layer: %q", byName["proj"].Layer)
	}
}
