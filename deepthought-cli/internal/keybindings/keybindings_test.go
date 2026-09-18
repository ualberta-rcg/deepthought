package keybindings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAndOverrides(t *testing.T) {
	m := Defaults()
	if got, ok := m.Resolve(Global, "f2"); !ok || got != Settings {
		t.Fatalf("f2=%q ok=%v", got, ok)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(`{"global":{"f2":"app:help"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Resolve(Global, "f2"); got != Help {
		t.Fatalf("override=%q", got)
	}
}

func TestReservedCannotBeRebound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	_ = os.WriteFile(path, []byte(`{"global":{"ctrl+c":"app:help"}}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("expected reserved-key error")
	}
}
