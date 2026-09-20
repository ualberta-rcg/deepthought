package tui

import (
	"strings"
	"testing"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/unimatrix"
)

// Section renders header + body + note + source in order, and omits note/source
// when they're empty.
func TestSectionRows(t *testing.T) {
	s := Section{
		Title:  "Widget",
		Extra:  "three things",
		Rows:   []string{"  alpha", "  beta"},
		Note:   "  about this widget",
		Source: "run-widget",
	}
	out := strings.Join(s.Render(), "\n")
	for _, want := range []string{"Widget", "three things", "alpha", "beta", "about this widget", "run-widget"} {
		if !strings.Contains(out, want) {
			t.Errorf("Section.Render() missing %q:\n%s", want, out)
		}
	}
	// order: title < note < source
	if i, n, src := strings.Index(out, "Widget"), strings.Index(out, "about this widget"), strings.Index(out, "run-widget"); i > n || n > src {
		t.Errorf("wrong order (title, note, source): %d,%d,%d", i, n, src)
	}

	// no note/source → neither appears
	bare := strings.Join(Section{Title: "Bare", Rows: []string{"  x"}}.Render(), "\n")
	if strings.Contains(bare, "→ ") {
		t.Errorf("bare section should have no source line: %q", bare)
	}
	if !strings.Contains(bare, "  x") {
		t.Errorf("bare section lost its body row: %q", bare)
	}
}

func TestStateChip(t *testing.T) {
	for state, word := range map[string]string{"ok": "ok", "degraded": "degraded", "idle": "idle", "weird": "idle"} {
		if !strings.Contains(stateChip(state), word) {
			t.Errorf("stateChip(%q) missing %q", state, word)
		}
	}
}

func TestHealthChip(t *testing.T) {
	if ok := healthChip(true, ""); !strings.Contains(ok, "reachable") || strings.Contains(ok, "✗") {
		t.Errorf("healthChip(true) = %q", ok)
	}
	if bad := healthChip(false, "timeout"); !strings.Contains(bad, "timeout") || strings.Contains(bad, "✓") {
		t.Errorf("healthChip(false) = %q", bad)
	}
}

func TestDetChip(t *testing.T) {
	if !strings.Contains(detChip(true), "✓") {
		t.Errorf("detChip(true) missing ✓")
	}
	if !strings.Contains(detChip(false), "✗") {
		t.Errorf("detChip(false) missing ✗")
	}
}

// contextMeter shows a bar + used/window with a known window, and degrades to a
// plain count without one.
func TestContextMeter(t *testing.T) {
	withWin := contextMeter(128000, 310000)
	if !strings.Contains(withWin, "▓") || !strings.Contains(withWin, "310k") {
		t.Errorf("contextMeter with window missing bar/window: %q", withWin)
	}
	noWin := contextMeter(128000, 0)
	if strings.Contains(noWin, "▓") || strings.Contains(noWin, "░") {
		t.Errorf("contextMeter without window should have no bar: %q", noWin)
	}
	if !strings.Contains(noWin, "128k") {
		t.Errorf("contextMeter without window missing count: %q", noWin)
	}
}

// The Usage section shows a live context meter when the active model declares a
// context window.
func TestUsageContextMeter(t *testing.T) {
	file := config.File{
		Models: []unimatrix.Model{{ID: "m1", Label: "M1", Provider: "p", Context: 310000}},
		Roles:  map[string]string{unimatrix.RoleAgentic: "m1"},
	}
	m := NewStatusModel(StatusInputs{Store: &fakeStore{saved: &file}})
	m = m.SetSession(0, 0, 128000, 0, 0)
	got := strings.Join(m.usageRows(), "\n")
	if !strings.Contains(got, "▓") || !strings.Contains(got, "310k") {
		t.Errorf("Usage context meter missing bar/window: %q", got)
	}
}
