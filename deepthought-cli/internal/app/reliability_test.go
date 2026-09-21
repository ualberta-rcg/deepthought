package app

import (
	"deepthought-cli/internal/config"
	"path/filepath"
	"testing"
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
