package app

import (
	"os"
	"path/filepath"
	"testing"

	"deepthought-cli/internal/config"
)

func TestSettingsRejectStaleSnapshot(t *testing.T) {
	s := NewSettings(config.Defaults(), filepath.Join(t.TempDir(), "config.json"))
	a, b := s.Snapshot(), s.Snapshot()
	a.Language = "fr"
	if err := s.Save(a); err != nil {
		t.Fatal(err)
	}
	b.Language = "en"
	if err := s.Save(b); err == nil {
		t.Fatal("stale edit overwrote settings")
	}
	if s.Snapshot().Language != "fr" {
		t.Fatal("first edit lost")
	}
}

func TestSettingsSnapshotDoesNotSharePointers(t *testing.T) {
	f := config.Defaults()
	on := true
	f.Thinking = &on
	f.Appearance = &config.Appearance{TopBarLegend: &on}
	s := NewSettings(f, filepath.Join(t.TempDir(), "config.json"))
	copy := s.Snapshot()
	*copy.Thinking = false
	*copy.Appearance.TopBarLegend = false
	if !*s.Snapshot().Thinking || !*s.Snapshot().Appearance.TopBarLegend {
		t.Fatal("snapshot mutated live settings")
	}
}

func TestSettingsWithoutLocalStoreStayInMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := NewSettings(config.Defaults(), path)
	f := s.Snapshot()
	f.Language = "fr"
	if err := s.Save(f); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Language != "fr" {
		t.Fatal("save not applied")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("settings without a local store wrote %s", path)
	}
	if err := s.Reload(); err != nil || s.Snapshot().Language != "fr" {
		t.Fatalf("reload without a store must keep the live config (err %v)", err)
	}
}
