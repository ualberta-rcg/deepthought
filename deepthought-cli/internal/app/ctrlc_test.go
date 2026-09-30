package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"deepthought-cli/internal/tui"
)

func ctrlC() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// TestCtrlCIsProgressive: the first press clears a typed prompt, only a
// second press on an empty prompt arms quit, and the third (within 2s) quits.
func TestCtrlCIsProgressive(t *testing.T) {
	m := sidebarRoot(t, 120, 40)
	m.screen = tui.ScreenChat
	m.chat = m.chat.SetInput("draft prompt")

	next, cmd := m.Update(ctrlC())
	m = next.(RootModel)
	if isQuit(cmd) || !m.chat.InputEmpty() {
		t.Fatal("first ctrl+c must clear the prompt, not quit")
	}
	next, cmd = m.Update(ctrlC())
	m = next.(RootModel)
	if isQuit(cmd) {
		t.Fatal("ctrl+c on an empty prompt must warn before quitting")
	}
	_, cmd = m.Update(ctrlC())
	if !isQuit(cmd) {
		t.Fatal("second ctrl+c on an empty prompt must quit")
	}
}
