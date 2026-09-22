package app

import (
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/tools"
	"deepthought-cli/internal/tui"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelFreeStartupAndNavigation(t *testing.T) {
	dir := t.TempDir()
	store, err := history.NewSQLiteStore(filepath.Join(dir, "history.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := NewRootModel(Deps{Live: NewSettings(config.Defaults(), filepath.Join(dir, "config.json")), Registry: tools.NewRegistry(), ChatSource: func() history.ChatStore { return store }, DisableMonitoring: true})
	for _, width := range []int{40, 80, 120, 180} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m = next.(RootModel)
		next, _ = m.Update(tui.SplashAdvanceMsg{})
		m = next.(RootModel)
		if m.screen != tui.ScreenChat {
			t.Fatal("missing model forced setup")
		}
		for _, line := range strings.Split(m.View().Content, "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("view overflows %d-column terminal", width)
			}
		}
		next, _ = m.Update(tui.WorkspaceAction{Kind: "menu"})
		m = next.(RootModel)
		if m.screen != tui.ScreenWorkspace || !strings.Contains(m.View().Content, "Settings") {
			t.Fatal("navigation unavailable without model")
		}
	}
}
