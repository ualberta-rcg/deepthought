package app

import (
	"path/filepath"
	"testing"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/keybindings"
	"deepthought-cli/internal/tools"
)

func sidebarRoot(t *testing.T, w, h int) RootModel {
	t.Helper()
	dir := t.TempDir()
	store, err := history.NewSQLiteStore(filepath.Join(dir, "history.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	m := NewRootModel(Deps{Live: NewSettings(config.Defaults(), filepath.Join(dir, "config.json")), Registry: tools.NewRegistry(), ChatSource: func() history.ChatStore { return store }, DisableMonitoring: true})
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(RootModel)
}

func pressSidebar(t *testing.T, m RootModel) RootModel {
	t.Helper()
	next, _ := m.handleAction(keybindings.Sidebar)
	return next.(RootModel)
}

func TestSidebarTogglesInOnePress(t *testing.T) {
	m := sidebarRoot(t, 160, 40)
	if !m.sidebarOn() {
		t.Fatal("auto sidebar should show on a 160×40 terminal")
	}
	m = pressSidebar(t, m)
	if m.sidebarOn() || m.deps.Live.SidebarMode() != "off" {
		t.Fatalf("one press should hide the sidebar (mode=%s)", m.deps.Live.SidebarMode())
	}
	m = pressSidebar(t, m)
	if !m.sidebarOn() || m.deps.Live.SidebarMode() != "on" {
		t.Fatalf("second press should show it again (mode=%s)", m.deps.Live.SidebarMode())
	}
}

func TestSidebarAutoAndOnThresholdsDiffer(t *testing.T) {
	m := sidebarRoot(t, 110, 40)
	if m.sidebarOn() {
		t.Fatal("auto should hide below 120 columns")
	}
	m = pressSidebar(t, m)
	if !m.sidebarOn() {
		t.Fatal("forcing on at 110 columns should show the sidebar beside a 60-column chat")
	}
	short := sidebarRoot(t, 160, 24)
	if short.sidebarOn() {
		t.Fatal("auto should hide below 30 rows")
	}
}

func TestSidebarAutoHideIsDerived(t *testing.T) {
	m := sidebarRoot(t, 160, 40)
	m = pressSidebar(t, m) // off
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = next.(RootModel)
	next, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m = next.(RootModel)
	if m.sidebarOn() || m.deps.Live.SidebarMode() != "off" {
		t.Fatal("growing the terminal must not clobber an explicit off")
	}

	m = pressSidebar(t, m) // on
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = next.(RootModel)
	if m.sidebarOn() {
		t.Fatal("sidebar should auto-hide when the chat would drop below 60 columns")
	}
	if m.deps.Live.SidebarMode() != "on" {
		t.Fatal("auto-hide must not be written back to the preference")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = next.(RootModel)
	if !m.sidebarOn() {
		t.Fatal("growing the terminal should restore the sidebar")
	}
}
