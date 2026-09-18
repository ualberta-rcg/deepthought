package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"

	"deepthought-cli/internal/slurm"
)

func testSnapshot() slurm.ClusterSnapshot {
	return slurm.ClusterSnapshot{
		FetchedAt:  time.Now(),
		NodesTotal: 247, NodesUp: 238,
		CPUAlloc: 7078, CPUTotal: 15808,
		GPUs: 980, GPUsUsed: 935, GPUUsable: 44, GPUType: "l40s",
		MemTotalGB: 5000, MemAllocGB: 2400,
		JobsRunning: 1048, JobsPending: 1093,
		Fairshare: 0.67,
		YourJobs: []slurm.Job{
			{ID: "100", Name: "train", State: "RUNNING", Elapsed: "01:20:00"},
			{ID: "101", Name: "eval", State: "PENDING", Reason: "Priority"},
		},
		FairshareRows: []slurm.FairshareRow{
			{Account: "aip-me", Fairshare: 0.67, LevelFS: "1.00"},
			{Account: "debug", Fairshare: 0.10, LevelFS: "inf"},
		},
		StorageRows: []slurm.StorageRow{
			{Label: "home", Used: "19G", Size: "50G", Pct: 37},
			{Label: "scratch", Used: "801G", Size: "5.0T", Pct: 16},
		},
	}
}

func TestClusterScreenRendersBlocks(t *testing.T) {
	m := NewClusterModel().Resize(100, 30).SetCluster(testSnapshot())
	v := m.View()
	for _, want := range []string{
		"Cluster", "Your jobs", "Fairshare", "Storage",
		"l40s", "avail", "usable", "held: Priority", "→ squeue --me",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("cluster view missing %q", want)
		}
	}
}

func TestClusterScreenNoSlurm(t *testing.T) {
	// Force detected=false regardless of the host (slurm is present on dev).
	m := ClusterModel{detected: false, vp: viewport.New()}.Resize(100, 30)
	if v := m.View(); !strings.Contains(v, "Slurm not detected") {
		t.Errorf("expected 'Slurm not detected': %q", v)
	}
}

func TestClusterBlurb(t *testing.T) {
	if got := (ChatModel{}).clusterBlurb(); got != "" {
		t.Fatalf("empty clusterBlurb = %q, want empty", got)
	}
	got := ChatModel{cluster: testSnapshot()}.clusterBlurb()
	for _, want := range []string{
		"GPUs 935/980", "44 usable", "fairshare 0.67", "ahead",
		"scratch 16% used", "stale",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("clusterBlurb missing %q:\n%s", want, got)
		}
	}
}
