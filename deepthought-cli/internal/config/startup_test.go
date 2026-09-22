package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewedDiscoveryNeverExecutesOrExports(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	_ = os.Mkdir(filepath.Join(project, ".git"), 0700)
	secret := "synthetic-canary-aleph-294838"
	_ = os.WriteFile(filepath.Join(home, ".aleph_tyk.env"), []byte("export TYK_KEY='"+secret+"'\n"), 0600)
	_ = os.WriteFile(filepath.Join(home, ".bashrc"), []byte("OPENAI_API_KEY=$(touch NEVER_EXECUTE)\nANTHROPIC_API_KEY=`cat private`\n"), 0600)
	_ = os.WriteFile(filepath.Join(project, ".env"), []byte("OPENAI_API_KEY=project-canary-123456\nOPENAI_BASE_URL=https://example.invalid/v1\n"), 0600)
	candidates := Discover(DiscoveryOptions{Home: home, WorkDir: project, LookupEnv: func(k string) string {
		if k == "TYK_KEY" {
			return "environment-canary-234567"
		}
		return ""
	}})
	if len(candidates) != 3 {
		t.Fatalf("want env, file, project candidates; got %d", len(candidates))
	}
	for _, c := range candidates {
		b, _ := json.Marshal(c)
		if strings.Contains(string(b), secret) || strings.Contains(fmt.Sprintf("%+v %#v", c, c), secret) {
			t.Fatal("candidate exposes credential")
		}
		if c.Name == "OpenAI" && !c.Untrusted {
			t.Fatal("project candidate must require untrusted review")
		}
	}
	if _, err := os.Stat(filepath.Join(home, "secrets.env")); !os.IsNotExist(err) {
		t.Fatal("discovery persisted secrets before adoption")
	}
	for _, c := range candidates {
		if strings.Contains(c.Source, ".aleph_tyk.env") {
			p, err := c.Adopt(filepath.Join(home, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if p.ExpandedKey() != secret {
				t.Fatal("adopted key does not resolve")
			}
			if p.BaseURL != DefaultBaseURL || strings.Contains(p.APIKey, secret) {
				t.Fatal("incorrect provider binding")
			}
		}
	}
}

func TestLiteralParserRejectsShellPrograms(t *testing.T) {
	input := []byte("export A='literal value' # ok\nB=$(bad)\nC=`bad`\nD=ok;bad\nE=\"ok\" && bad\nF=$OTHER\nG=plain\n")
	got := LiteralAssignments(input)
	if len(got) != 2 || got["A"] != "literal value" || got["G"] != "plain" {
		t.Fatalf("unexpected literal parser result: %v", got)
	}
}

func TestLocalSettingsMigrationAndRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEEPTHOUGHT_CLI_HOME", dir)
	path := filepath.Join(dir, "config.json")
	secret := "migration-canary-credentials-9921"
	raw := `{"providers":[{"name":"custom","base_url":"https://custom.invalid/v1","api_key":"` + secret + `"}],"models":[],"language":"fr"}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := OpenLocal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != "fr" || cfg.Providers[0].ExpandedKey() != secret {
		t.Fatal("migration lost configuration")
	}
	var saved string
	if err := cfg.Local.db.QueryRow("SELECT body FROM app_settings").Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(saved, secret) {
		t.Fatal("database contains literal credential")
	}
	backup, _ := os.ReadFile(path + ".before-database")
	if string(backup) != raw {
		t.Fatal("original settings not preserved")
	}
	f := cfg.File
	f.Language = "en"
	next, err := cfg.Local.Save(cfg.File, f)
	if err != nil {
		t.Fatal(err)
	}
	stale := cfg.File
	stale.Language = "de"
	if _, err = cfg.Local.Save(next.File, stale); err == nil {
		t.Fatal("stale writer accepted")
	}
	_ = cfg.Local.Close()
	cfg, err = OpenLocal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Local.Close()
	if cfg.Language != "en" {
		t.Fatal("restart reimported stale JSON")
	}
	portable, _ := json.Marshal(Portable(cfg.File))
	if strings.Contains(string(portable), secret) || strings.Contains(string(portable), "secret:") {
		t.Fatal("portable settings expose credentials")
	}
}

func TestInvalidSettingsRecoverWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEEPTHOUGHT_CLI_HOME", dir)
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte("{broken"), 0600)
	cfg, err := OpenLocal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Local.Close()
	if cfg.StartupNotice == "" || len(cfg.Models) != 0 {
		t.Fatal("missing recoverable startup state")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "{broken" {
		t.Fatal("invalid original overwritten")
	}
}

func TestLocalPrecedenceAndSessionOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEEPTHOUGHT_CLI_HOME", dir)
	t.Setenv("DEEPTHOUGHT_EFFORT", "high")
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"effort":"low"}`), 0600)
	cfg, err := OpenLocal(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Local.Close()
	if cfg.Effort != "high" || cfg.Source("effort") != "environment" {
		t.Fatal("environment override must be temporary above saved profile")
	}
	f := cfg.File
	f.Effort = "medium"
	cfg, err = cfg.Local.Save(cfg.File, f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Effort != "medium" || cfg.Source("effort") != "session" {
		t.Fatal("explicit UI edit must take effect for the session")
	}
}
