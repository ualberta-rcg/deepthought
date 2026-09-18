package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAndMergeClaudeProviders(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"env":{
		"ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic",
		"ANTHROPIC_AUTH_TOKEN":"secret",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.2",
		"API_TIMEOUT_MS":"1234"
	}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates, err := DiscoverClaudeProviders(home)
	if err != nil {
		t.Fatal(err)
	}
	var zai *ImportCandidate
	for i := range candidates {
		if candidates[i].Provider.Name == "Z.ai" {
			zai = &candidates[i]
		}
	}
	if zai == nil || zai.Provider.APIKey != "$ANNORAX_Z_AI_API_KEY" {
		t.Fatalf("candidates = %+v", candidates)
	}
	candidates = []ImportCandidate{*zai}
	file := Defaults().File
	configPath := filepath.Join(t.TempDir(), "config.json")
	added, err := MergeImports(&file, candidates, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || os.Getenv("ANNORAX_Z_AI_API_KEY") != "secret" {
		t.Fatalf("added=%d env=%q", added, os.Getenv("ANNORAX_Z_AI_API_KEY"))
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(configPath), "secrets.env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets mode = %o", info.Mode().Perm())
	}
}
