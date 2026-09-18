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
	writeSkill(t, filepath.Join(project, ".annorax", "skills"), "slurm", "slurm", "project")
	writeSkill(t, filepath.Join(home, ".annorax", "skills"), "slurm", "slurm", "user")
	if err := os.MkdirAll(filepath.Join(project, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(site)
	loader.UserHome = home
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
	loader.UserHome = home
	skills, err := loader.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "alliance-slurm" || len(skills[0].AllowedTools) != 1 {
		t.Fatalf("skills=%+v", skills)
	}
}
