package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"deepthought-cli/internal/queen"
	"deepthought-cli/internal/tools"
)

func TestSplashRequiresExplicitChoice(t *testing.T) {
	m := NewSplashModel(BootInfo{}, "fixture").Resize(80, 24)
	if !strings.Contains(splashANSI.ReplaceAllString(m.View(), ""), "Run standalone") {
		t.Fatal("missing standalone choice")
	}
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd != nil {
		t.Fatal("arbitrary key selected a mode")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("server choice should emit a login command")
	}
	if splashANSI.ReplaceAllString(m.View(), "") != "" && strings.Contains(splashANSI.ReplaceAllString(m.View(), ""), "coming soon") {
		t.Fatal("server choice should be functional, not 'coming soon'")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("standalone not selected")
	}
}

func TestInterruptedApprovalAndStaleEvents(t *testing.T) {
	m := NewChatModel(fakeSource{}, tools.NewRegistry(tools.NewBash()), queen.NewGate(queen.Review), "test", fileSource(t.TempDir()))
	m.coll.StartIncursion("work")
	m.busy = true
	m.ctx, m.cancel = context.WithCancel(context.Background())
	ctx := m.ctx
	m.awaiting = &pendingApproval{}
	old := chatEvent{owner: m.coll.ID, turn: m.generation, msg: streamItemMsg{delta: "stale"}}
	m, _ = m.interrupt()
	if ctx.Err() == nil || m.awaiting != nil {
		t.Fatal("interruption retained execution or approval")
	}
	m.busy = true // a subsequent turn must not accept the old result
	m, _ = m.Update(old)
	if m.acc != "" {
		t.Fatal("stale stream changed the next turn")
	}
}

func TestPasswordEditorMasksValue(t *testing.T) {
	e := newTextEdit("api key", "fixture-secret", true)
	if strings.Contains(e.input.View(), "fixture-secret") {
		t.Fatal("secret rendered in plaintext")
	}
}

func TestCronAddBeforeLoadAndCancel(t *testing.T) {
	m := NewCronModel(t.TempDir())
	m, _ = m.beginAdd()
	if m.edit != nil {
		t.Fatal("editing before loaded")
	}
	m.stage()
	m, _ = m.beginAdd()
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.dirty() {
		t.Fatal("cancelled addition changed crontab")
	}
}
