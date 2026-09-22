package tui

import (
	"strings"
	"testing"
	"time"

	"deepthought-cli/internal/slurm"
)

func testSnapshot() slurm.ClusterSnapshot {
	return slurm.ClusterSnapshot{
		FetchedAt:  time.Now(),
		NodesTotal: 247, NodesUp: 238,
		CPUAlloc: 7078, CPUTotal: 15808,
		GPUs: 980, GPUsUsed: 935, GPUUsable: 44, GPUType: "l40s",
		MemTotalGB: 5000, MemAllocGB: 2400,
		GPUAllocKnown: true, MemoryAllocKnown: true, QueueKnown: true,
		ClusterRunning: 1048, ClusterPending: 1093,
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

// The Cluster section renderers (shown by the Status page) emit the full
// vulcan-status blocks when the snapshot has data.
func TestClusterSectionRenderers(t *testing.T) {
	s := testSnapshot()
	cases := map[string][]string{
		"cluster":   renderClusterBlock(s),
		"jobs":      renderJobsBlock(s),
		"fairshare": renderFairshareBlock(s),
		"dirs":      renderStorageBlock(s),
	}
	want := map[string]string{
		"cluster":   "l40s",
		"jobs":      "held: Priority",
		"fairshare": "0.67",
		"dirs":      "scratch",
	}
	for name, rows := range cases {
		if !strings.Contains(strings.Join(rows, "\n"), want[name]) {
			t.Errorf("%s block missing %q:\n%s", name, want[name], strings.Join(rows, "\n"))
		}
	}
}

func TestClusterBlurb(t *testing.T) {
	if got := (ChatModel{}).clusterBlurb(); got != "" {
		t.Fatalf("empty clusterBlurb = %q, want empty", got)
	}
	got := ChatModel{cluster: testSnapshot()}.clusterBlurb()
	for _, want := range []string{
		"GPUs 935/980 allocated", "fairshare 0.67", "scheduling factor", "cluster queue 1048 running / 1093 pending",
		"scratch 16% used", "stale",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("clusterBlurb missing %q:\n%s", want, got)
		}
	}
}

func TestClusterUnknownAllocationRemainsUnknown(t *testing.T) {
	s := testSnapshot()
	s.GPUAllocKnown, s.MemoryAllocKnown, s.QueueKnown = false, false, false
	got := strings.Join(renderClusterBlock(s), "\n")
	if strings.Count(got, "allocation unavailable") != 2 || !strings.Contains(got, "queue unavailable") {
		t.Fatal(got)
	}
	brief := (ChatModel{cluster: s}).clusterBlurb()
	if strings.Contains(brief, "usable now") || strings.Contains(brief, "935/980") || strings.Contains(brief, "cluster queue") {
		t.Fatal("unknown allocation presented as known", brief)
	}
}
