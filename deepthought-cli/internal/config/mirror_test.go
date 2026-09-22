package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMirrorRegeneratesAndProtectsExternalEdits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEEPTHOUGHT_CLI_HOME", dir)
	path := filepath.Join(dir, "config.json")
	cfg, err := OpenLocal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Local.Close()
	if _, err = os.Stat(path); err != nil {
		t.Fatal("fresh configuration file missing", err)
	}
	f := cfg.File
	f.Providers = []Provider{{Name: "test", BaseURL: "https://provider.invalid/v1", APIKey: "synthetic-mirror-secret"}}
	cfg, err = cfg.Local.Save(cfg.File, f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "synthetic-mirror-secret") || !strings.Contains(string(raw), "secret:") {
		t.Fatal("credential separation failed")
	}
	var disk File
	if err = json.Unmarshal(raw, &disk); err != nil || len(disk.Providers) != 1 {
		t.Fatal("provider not saved to file")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	cfg.Local.RepairMirror()
	if _, err = os.Stat(path); err != nil {
		t.Fatal("missing mirror not repaired")
	}
	external := []byte(`{"language":"fr","providers":[],"models":[]}`)
	if err = os.WriteFile(path, external, 0600); err != nil {
		t.Fatal(err)
	}
	f = cfg.File
	f.Language = "en"
	cfg, err = cfg.Local.Save(cfg.File, f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if string(raw) != string(external) || cfg.Local.MirrorNotice() == "" {
		t.Fatal("external edit lost or no notice")
	}
	cfg, err = cfg.Local.ImportFile()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != "fr" || len(cfg.Providers) != 0 || cfg.Local.MirrorNotice() != "" {
		t.Fatal("reviewed import failed")
	}
}
func TestFailedMirrorWriteRetainsDatabaseAndRepairs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEEPTHOUGHT_CLI_HOME", dir)
	path := filepath.Join(dir, "config.json")
	cfg, err := OpenLocal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Local.Close()
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	f := cfg.File
	f.Language = "fr"
	cfg, err = cfg.Local.Save(cfg.File, f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != "fr" || cfg.Local.MirrorNotice() == "" {
		t.Fatal("database save lost or file failure hidden")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	cfg.Local.RepairMirror()
	restored, err := Load(path)
	if err != nil || restored.Language != "fr" {
		t.Fatal("mirror repair failed", err)
	}
}
func TestSharedCredentialsAndEndpointRebinding(t *testing.T) {
	f := Defaults().File
	f.Providers = []Provider{{Name: "a", BaseURL: "https://a.invalid/v1", APIKey: "secret:PRIVATE"}}
	doc := Shared(f)
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "api_key") {
		t.Fatal("shared credential")
	}
	changed := Defaults().File
	changed.Providers = []Provider{{Name: "a", BaseURL: "https://b.invalid/v1"}}
	merged, err := ApplyShared(f, Shared(changed))
	if err != nil {
		t.Fatal(err)
	}
	if merged.Providers[0].APIKey != "" {
		t.Fatal("key rebound to another endpoint")
	}
}
func TestSharedMergeIndependentChangesConflictsAndDeletion(t *testing.T) {
	base := Shared(Defaults().File)
	lf, rf := Defaults().File, Defaults().File
	lf.Providers = []Provider{{Name: "a", BaseURL: "https://a.invalid"}}
	rf.Providers = []Provider{{Name: "b", BaseURL: "https://b.invalid"}}
	merged, conflicts := MergeShared(base, Shared(lf), Shared(rf), nil)
	if len(conflicts) != 0 || len(merged["providers"].([]any)) != 2 {
		t.Fatal("independent additions not merged")
	}
	lf.Language = "fr"
	rf.Language = "de"
	_, conflicts = MergeShared(base, Shared(lf), Shared(rf), nil)
	if len(conflicts) != 1 {
		t.Fatal("conflict not found")
	}
	c := conflicts[0]
	resolved, conflicts := MergeShared(base, Shared(lf), Shared(rf), map[string]SyncChoice{c.Path: {Side: "remote", Fingerprint: c.Fingerprint()}})
	if len(conflicts) != 0 || resolved["language"] != "de" {
		t.Fatal("resolution failed")
	}
	empty := Defaults().File
	empty.Language = "de"
	result, conflicts := MergeShared(resolved, Shared(empty), resolved, nil)
	if len(conflicts) != 0 || len(result["providers"].([]any)) != 0 {
		t.Fatal("deleted providers resurrected")
	}
}
