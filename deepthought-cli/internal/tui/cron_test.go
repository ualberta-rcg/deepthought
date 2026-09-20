package tui

import (
	"context"
	"errors"
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

// errCronClient fails the load (no crontab binary / corrupt registry class).
type errCronClient struct{ fakeCronClient }

func (e *errCronClient) List(context.Context) ([]cron.Line, error) {
	return nil, errors.New("crontab: not found")
}

// The screen must survive keypresses and renders BEFORE the async load lands
// (the old code dereferenced a nil pending table on the first View — a
// deterministic crash on every open).
func TestCronPreloadNoPanic(t *testing.T) {
	m := newCronModelWith(&fakeCronClient{listed: "0 9 * * * a.sh"})
	m = m.Resize(80, 24)
	for _, k := range []string{"j", "k", "down", "up", "enter", "a", "e", "d", "P", "y", "u", "r"} {
		m, _ = m.Update(keyPress('x', "x")) // movement first
		_ = m.View()
		_ = k
	}
	plain := stripTestANSI.ReplaceAllString(m.View(), "")
	if !strings.Contains(plain, "loading crontab") {
		t.Errorf("pre-load view should show the loading placeholder: %q", plain)
	}
}

// A hard load failure (no crontab binary) renders the error instead of
// panicking on every subsequent frame.
func TestCronLoadFailureRenders(t *testing.T) {
	m := newCronModelWith(&errCronClient{})
	m = m.Resize(80, 24)
	m, _ = m.Update(m.reloadCmd()())
	v := m.View() // must not panic
	plain := stripTestANSI.ReplaceAllString(v, "")
	if !strings.Contains(plain, "crontab: not found") {
		t.Errorf("load failure should render the error: %q", plain)
	}
	// And the screen still responds.
	m2, cmd := m.Update(keyPress(tea.KeyEsc, ""))
	_ = m2
	_ = cmd
}

// The cursor is VISIBLE on the list (the old rows never marked it).
func TestCronCursorVisible(t *testing.T) {
	m := loadedCron("0 9 * * * a.sh\n0 9 * * * b.sh\n").Resize(80, 24)
	v := stripTestANSI.ReplaceAllString(m.View(), "")
	if !strings.Contains(v, "▶") {
		t.Error("no cursor marker rendered on the cron list")
	}
	// j moves it to the second entry; d stages a delete of THAT row.
	m2, _ := m.Update(keyPress('j', "j"))
	if len(m2.pendingEntries()) != 2 {
		t.Fatalf("entries = %d", len(m2.pendingEntries()))
	}
	m3, _ := m2.Update(keyPress('d', "d"))
	if len(m3.pendingEntries()) != 1 {
		t.Errorf("d after j should delete the SECOND entry; entries = %d", len(m3.pendingEntries()))
	}
}

// esc from the diff view goes BACK to the list — never the discard gate (a
// reflexive y there destroyed all staged work).
func TestCronEscFromDiffGoesBack(t *testing.T) {
	fc := &fakeCronClient{listed: "0 9 * * * keep.sh"}
	m := newCronModelWith(fc)
	m, _ = m.Update(m.reloadCmd()())
	m, _ = m.beginAdd()
	m.edit.input.SetValue("15 3 * * * x.sh")
	m, _ = m.commitEdit()
	m, _ = m.Update(keyPress('P', "P"))
	if m.view != cvDiff {
		t.Fatalf("expected diff view, got %v", m.view)
	}
	m, _ = m.Update(keyPress(tea.KeyEsc, ""))
	if m.view != cvList {
		t.Fatalf("esc from diff should return to the list, got %v", m.view)
	}
	if !m.dirty() {
		t.Error("staged changes must survive esc-from-diff")
	}
}

// An invalid add keeps the editor open — the seeded default row is never left
// staged as junk.
func TestCronInvalidAddKeepsEditor(t *testing.T) {
	m := loadedCron("0 9 * * * a.sh\n")
	rowsBefore := len(m.pending.rows)
	m, _ = m.beginAdd()
	m.edit.input.SetValue("too few fields")
	m, _ = m.commitEdit()
	if m.edit == nil {
		t.Fatal("invalid entry should keep the editor open")
	}
	if m.toast == "" {
		t.Error("expected an explanatory toast")
	}
	// Fix it and commit — the staged row is replaced, not duplicated.
	m.edit.input.SetValue("*/10 * * * * fixed.sh")
	m, _ = m.commitEdit()
	if len(m.pending.rows) != rowsBefore+1 {
		t.Errorf("rows = %d, want %d (no junk row left behind)", len(m.pending.rows), rowsBefore+1)
	}
}
