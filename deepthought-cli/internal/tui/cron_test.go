package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/cron"
)

// fakeCronClient fakes the crontab + registry for screen tests.
type fakeCronClient struct {
	listed    string
	installs  [][]string
	undones   int
	registry  cron.Registry
	saveCalls int
}

func (f *fakeCronClient) List(context.Context) ([]cron.Line, error) {
	return cron.Parse(f.listed), nil
}
func (f *fakeCronClient) Install(_ context.Context, lines []string) error {
	f.installs = append(f.installs, lines)
	f.listed = strings.Join(lines, "\n")
	return nil
}
func (f *fakeCronClient) Undo(context.Context) error {
	f.undones++
	return nil
}
func (f *fakeCronClient) LoadRegistry() (cron.Registry, error) { return f.registry, nil }
func (f *fakeCronClient) SaveRegistry(r cron.Registry) error {
	f.saveCalls++
	f.registry = r
	return nil
}

func loadedCron(listed string) CronModel {
	m := newCronModelWith(&fakeCronClient{listed: listed})
	m, _ = m.Update(m.reloadCmd()()) // synchronous fake: feed the loaded msg
	return m
}

// The screen lists the humanized crontab and fits 80 columns.
func TestCronScreenRenders(t *testing.T) {
	m := loadedCron("0 9 * * * /home/me/daily.sh\n*/5 * * * * sync.sh\n").Resize(80, 24)
	v := m.View()
	plain := stripTestANSI.ReplaceAllString(v, "")
	for _, want := range []string{"Your crontab", "daily 09:00", "every 5 min", "sync.sh"} {
		if !strings.Contains(plain, want) {
			t.Errorf("view missing %q", want)
		}
	}
	for i, ln := range strings.Split(v, "\n") {
		if w := lipgloss.Width(ln); w > 80 {
			t.Errorf("line %d width %d > 80", i, w)
		}
	}
}

// add → diff → y → confirm y installs exactly once, through the client.
func TestCronAddDiffApplyFlow(t *testing.T) {
	fc := &fakeCronClient{listed: "0 9 * * * old.sh"}
	m := newCronModelWith(fc)
	m, _ = m.Update(m.reloadCmd()())

	// Add: editor opens pre-seeded; replace its value and commit.
	m, _ = m.beginAdd()
	m.edit.input.SetValue("15 3 * * * backup.sh")
	m, _ = m.commitEdit()
	if !m.dirty() {
		t.Fatal("add should stage a change")
	}

	// P → diff; y → confirm; not-y aborts; y applies.
	m, _ = m.Update(keyPress('P', "P"))
	m, _ = m.Update(keyPress('y', "y")) // from diff: enter the confirm gate
	m, _ = m.Update(keyPress('n', "n")) // anything but y aborts
	if len(fc.installs) != 0 {
		t.Fatal("non-y key must not apply")
	}
	// Re-enter after the abort: P → diff, y → confirm.
	m, _ = m.Update(keyPress('P', "P"))
	m, _ = m.Update(keyPress('y', "y"))
	var cmd tea.Cmd
	m, cmd = m.Update(keyPress('y', "y"))
	if cmd != nil {
		m, _ = m.Update(cmd()) // drain the async apply
	}
	if len(fc.installs) != 1 {
		t.Fatalf("applies = %d, want 1", len(fc.installs))
	}
	if !strings.Contains(strings.Join(fc.installs[0], "\n"), "backup.sh") {
		t.Errorf("installed table = %v", fc.installs[0])
	}
}

// esc with staged changes prompts; y discards without touching the crontab.
func TestCronDiscardGate(t *testing.T) {
	fc := &fakeCronClient{listed: "0 9 * * * keep.sh"}
	m := newCronModelWith(fc)
	m, _ = m.Update(m.reloadCmd()())
	m, _ = m.beginAdd()
	m.edit.input.SetValue("0 0 * * * x.sh")
	m, _ = m.commitEdit()
	m, _ = m.Update(keyPress(tea.KeyEsc, ""))
	if m.view != cvDiscard {
		t.Fatalf("esc with staged changes should prompt, view=%v", m.view)
	}
	m, _ = m.Update(keyPress('y', "y"))
	if len(fc.installs) != 0 {
		t.Error("discard must not install")
	}
	if m.dirty() {
		t.Error("discard should clear the pending table")
	}
}

// Undo goes through the same y/N gate.
func TestCronUndoGated(t *testing.T) {
	fc := &fakeCronClient{listed: "0 9 * * * a.sh"}
	m := newCronModelWith(fc)
	m, _ = m.Update(m.reloadCmd()())
	m, _ = m.Update(keyPress('u', "u"))
	if m.view != cvConfirm {
		t.Fatal("undo should enter the confirm gate")
	}
	var cmd tea.Cmd
	m, cmd = m.Update(keyPress('y', "y"))
	if cmd != nil {
		m, _ = m.Update(cmd()) // drain the async undo
	}
	if fc.undones != 1 {
		t.Errorf("undones = %d, want 1", fc.undones)
	}
}
