package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tui/kit"
)

// statusPage renders a StatusModel on a tall terminal so every section fits
// without viewport clipping — used to assert which detection-gated sections show.
func statusPage(t *testing.T, env EnvInfo, snap *slurm.ClusterSnapshot) string {
	t.Helper()
	m := NewStatusModel(StatusInputs{Store: &fakeStore{}, Env: env})
	if snap != nil {
		m = m.SetCluster(*snap)
	}
	return m.Resize(100, 200).View()
}

// The Status page is detection-driven: always-on sections always render, Slurm
// sections only when Slurm is detected and gathered, and the fairshare/dir
// sections additionally only when they have rows.
func TestStatusAdaptiveSections(t *testing.T) {
	// 1) No Slurm: only the always-on sections; no cluster / dirs sections.
	no := statusPage(t, EnvInfo{Host: "login1", User: "rahimk"}, nil)
	for _, want := range []string{"Session", "Host"} {
		if !strings.Contains(no, want) {
			t.Errorf("no-slurm page missing %q:\n%s", want, no)
		}
	}
	for _, absent := range []string{"Cluster", "Your jobs", "Fairshare", "Filesystem capacity"} {
		if strings.Contains(no, absent) {
			t.Errorf("no-slurm page should not show %q:\n%s", absent, no)
		}
	}

	// 2) Slurm detected + full snapshot: every section present.
	snap := testSnapshot()
	full := statusPage(t, EnvInfo{Slurm: true, Host: "login1", User: "rahimk"}, &snap)
	for _, want := range []string{"Session", "Host", "Cluster", "Your jobs", "Fairshare", "Filesystem capacity"} {
		if !strings.Contains(full, want) {
			t.Errorf("slurm page missing %q:\n%s", want, full)
		}
	}

	// 3) Slurm detected but no fairshare/storage rows: those two drop, the rest
	//    (cluster + your jobs) stay.
	slim := testSnapshot()
	slim.FairshareRows = nil
	slim.StorageRows = nil
	slimView := statusPage(t, EnvInfo{Slurm: true, Host: "login1", User: "rahimk"}, &slim)
	for _, want := range []string{"Cluster", "Your jobs"} {
		if !strings.Contains(slimView, want) {
			t.Errorf("slim slurm page missing %q:\n%s", want, slimView)
		}
	}
	for _, absent := range []string{"Fairshare", "Filesystem capacity"} {
		if strings.Contains(slimView, absent) {
			t.Errorf("slim slurm page should not show %q:\n%s", absent, slimView)
		}
	}
}

func TestStatusCardsFitAndFlowIntoColumns(t *testing.T) {
	snap := testSnapshot()
	env := EnvInfo{Slurm: true, Host: "login1", User: "rahimk"}
	for _, w := range []int{60, 80, 120, 160} {
		m := NewStatusModel(StatusInputs{Store: &fakeStore{}, Env: env}).SetCluster(snap).Resize(w, 200)
		view := m.View()
		twoCols := false
		for _, ln := range strings.Split(view, "\n") {
			if got := ansi.StringWidth(ln); got > w {
				t.Fatalf("width %d: line is %d cells: %q", w, got, ansi.Strip(ln))
			}
			if strings.Count(ansi.Strip(ln), "╭") == 2 {
				twoCols = true
			}
		}
		if want := w-4 >= statusTwoColumnW; twoCols != want {
			t.Errorf("width %d: two columns = %v, want %v", w, twoCols, want)
		}
		for _, card := range []string{"Session", "Host", "Your jobs", "Fairshare"} {
			if !strings.Contains(view, " "+card+" ") {
				t.Errorf("width %d: missing card %q", w, card)
			}
		}
	}
}

func TestStatusASCII(t *testing.T) {
	kit.SetASCII(true)
	defer kit.SetASCII(false)
	snap := testSnapshot()
	m := NewStatusModel(StatusInputs{Store: &fakeStore{}, Env: EnvInfo{Slurm: true, Host: "login1"}}).SetCluster(snap).Resize(100, 200)
	body := ansi.Strip(m.vp.View())
	for _, glyph := range []string{"╭", "│", "✓", "✗", "→"} {
		if strings.Contains(body, glyph) {
			t.Errorf("ASCII status body contains %q", glyph)
		}
	}
}
