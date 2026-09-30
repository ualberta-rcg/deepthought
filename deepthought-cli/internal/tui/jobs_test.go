package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"deepthought-cli/internal/history"
	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/workflow"
)

func fitsWidth(t *testing.T, view string, w int) {
	t.Helper()
	for _, ln := range strings.Split(view, "\n") {
		if got := ansi.StringWidth(ln); got > w {
			t.Fatalf("line is %d cells, terminal %d: %q", got, w, ansi.Strip(ln))
		}
	}
}

func typeKeys(m JobsModel, keys ...string) JobsModel {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		default:
			msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		}
		m, _ = m.Update(msg)
	}
	return m
}

func TestJobsListAndDetail(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	m := NewJobsModel(nil)
	m, _ = m.Update(jobsLoadedMsg{items: []slurm.Submission{
		{ID: "alpha-run", JobID: "101", State: "RUNNING", ScriptHash: "abc", Updated: now},
		{ID: "beta-run", State: "TIMEOUT", ScriptHash: "def", Updated: now},
	}})
	for _, size := range [][2]int{{120, 30}, {80, 24}, {50, 14}} {
		m = m.Resize(size[0], size[1])
		v := m.View()
		fitsWidth(t, v, size[0])
		if !strings.Contains(v, "Submission alpha-run") {
			t.Fatalf("%v: detail for the selection missing:\n%s", size, ansi.Strip(v))
		}
	}
	m = m.Resize(100, 30)
	if v := ansi.Strip(typeKeys(m, "down").View()); !strings.Contains(v, "Submission beta-run") || !strings.Contains(v, "longer walltime") {
		t.Fatalf("down should select beta-run with its retry advice:\n%s", v)
	}
	m = typeKeys(m, "/", "b", "e")
	if !m.CapturingKeys() || !strings.Contains(ansi.Strip(m.View()), "Submission beta-run") {
		t.Fatalf("filter 'be' should select beta-run:\n%s", ansi.Strip(m.View()))
	}
	m = typeKeys(m, "esc")
	if m.CapturingKeys() || m.list.Filter != "" {
		t.Fatal("esc should clear and leave the filter")
	}
}

func TestJobsAndPlansEmpty(t *testing.T) {
	j := NewJobsModel(nil).Resize(80, 20)
	j, _ = j.Update(jobsLoadedMsg{})
	if v := ansi.Strip(j.View()); !strings.Contains(v, jobsEmpty) || !strings.Contains(v, "needs your approval") {
		t.Fatalf("jobs empty state:\n%s", v)
	}
	p := NewPlansModel(nil).Resize(80, 20)
	p, _ = p.Update(plansLoadedMsg{})
	if v := ansi.Strip(p.View()); !strings.Contains(v, plansEmpty) || !strings.Contains(v, "success predicates") {
		t.Fatalf("plans empty state:\n%s", v)
	}
}

func TestPlansDetail(t *testing.T) {
	d := &history.Directive{Title: "Fit spectra", Status: "active", Version: 2}
	d.ID = "dir-1"
	p := NewPlansModel(nil).Resize(90, 24)
	p, _ = p.Update(plansLoadedMsg{plans: []workflow.Plan{{Directive: d}}})
	v := ansi.Strip(p.View())
	fitsWidth(t, p.View(), 90)
	if !strings.Contains(v, "Fit spectra") || !strings.Contains(v, "No objectives yet") || !strings.Contains(v, "Artifacts: 0") {
		t.Fatalf("plan detail:\n%s", v)
	}
}
