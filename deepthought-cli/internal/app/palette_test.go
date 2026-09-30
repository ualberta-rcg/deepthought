package app

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/tools"
	"deepthought-cli/internal/tui"
)

func paletteRoot(t *testing.T) RootModel {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEEPTHOUGHT_CLI_HOME", dir)
	cfg, err := config.OpenLocal(filepath.Join(dir, "config.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cfg.Local.Close() })
	store, err := history.NewSQLiteStore(filepath.Join(dir, "chats.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	m := NewRootModel(Deps{Live: NewSettings(cfg, filepath.Join(dir, "config.json")), Registry: tools.NewRegistry(), ChatSource: func() history.ChatStore { return store }, DisableMonitoring: true})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(RootModel)
	m.screen = tui.ScreenChat
	return m
}

func TestCtrlPOpensPaletteWithBoundKeys(t *testing.T) {
	m := paletteRoot(t)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = next.(RootModel)
	if _, ok := m.overlay.(tui.PaletteModel); !ok {
		t.Fatalf("ctrl+p opened %T, want the palette", m.overlay)
	}
	var model tui.PaletteEntry
	for _, e := range m.paletteEntries() {
		if e.ID == "app:model" {
			model = e
		}
	}
	if model.Key != "F3" {
		t.Fatalf("model entry key = %q, want F3", model.Key)
	}
}

func TestPaletteRecentsPersistAndLead(t *testing.T) {
	m := paletteRoot(t)
	next, _ := m.Update(tui.PaletteChosenMsg{Entry: tui.PaletteEntry{Kind: "slash", ID: "pin"}})
	m = next.(RootModel)
	if got := m.chat.Input(); got != "/pin " {
		t.Fatalf("slash with arguments should prefill the prompt, got %q", got)
	}
	next, _ = m.Update(tui.PaletteChosenMsg{Entry: tui.PaletteEntry{Kind: "home"}})
	m = next.(RootModel)
	refs := m.paletteRecents()
	if len(refs) != 2 || refs[0] != "home:" || refs[1] != "slash:pin" {
		t.Fatalf("recents = %v", refs)
	}
	p := tui.NewPalette(m.paletteEntries(), refs)
	if e, ok := p.Selected(); !ok || e.Kind != "home" {
		t.Fatalf("most recent entry should lead, got %+v", e)
	}
}
