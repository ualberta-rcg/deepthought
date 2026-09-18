package tui

import (
	"strings"
	"testing"

	"deepthought-cli/internal/slurm"
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
	for _, want := range []string{"Session", "Login node"} {
		if !strings.Contains(no, want) {
			t.Errorf("no-slurm page missing %q:\n%s", want, no)
		}
	}
	for _, absent := range []string{"Cluster", "Your jobs", "Fairshare", "Your dirs"} {
		if strings.Contains(no, absent) {
			t.Errorf("no-slurm page should not show %q:\n%s", absent, no)
		}
	}

	// 2) Slurm detected + full snapshot: every section present.
	snap := testSnapshot()
	full := statusPage(t, EnvInfo{Slurm: true, Host: "login1", User: "rahimk"}, &snap)
	for _, want := range []string{"Session", "Login node", "Cluster", "Your jobs", "Fairshare", "Your dirs"} {
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
	for _, absent := range []string{"Fairshare", "Your dirs"} {
		if strings.Contains(slimView, absent) {
			t.Errorf("slim slurm page should not show %q:\n%s", absent, slimView)
		}
	}
}
