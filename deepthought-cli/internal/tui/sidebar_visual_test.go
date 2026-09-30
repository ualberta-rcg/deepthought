package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"deepthought-cli/internal/host"
	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tui/kit"
)

func visualFixture() SidebarData {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	return SidebarData{Clock: now, ClusterOK: true, Env: EnvInfo{Slurm: true, Observation: host.Record{Name: "research-box", CollectedAt: now.Add(-12 * time.Second), CPUKnown: true, CPUPercent: 42, MemoryKnown: true, MemoryTotal: 256 << 30, MemoryUsed: 98 << 30, GPUs: []host.GPU{{Name: "Example GPU", UtilKnown: true, Utilization: 26, MemoryKnown: true, MemoryTotalMiB: 48 * 1024, MemoryUsedMiB: 8 * 1024}}}}, Cluster: slurm.ClusterSnapshot{ClusterName: "research-cluster", FetchedAt: now, QueueKnown: true, ClusterRunning: 24, ClusterPending: 8, CPUTotal: 512, CPUAlloc: 240, GPUs: 32, GPUsUsed: 20, GPUAllocKnown: true, MemTotalGB: 4096, MemAllocGB: 1400, MemoryAllocKnown: true, FairshareRows: []slurm.FairshareRow{{Account: "project-example", Fairshare: .64}}, JobsKnown: true, JobsRunning: 1, JobsPending: 1, YourJobs: []slurm.Job{{ID: "123", Name: "analysis", State: "RUNNING", Elapsed: "00:18"}, {ID: "124", Name: "training", State: "PENDING", Reason: "Resources"}}}}
}
func TestSidebarFixtures(t *testing.T) {
	d := visualFixture()
	var output strings.Builder
	for _, size := range []struct{ w, h int }{{44, 24}, {32, 18}, {44, 10}, {60, 35}} {
		rendered := RenderSidebar(d, size.w, size.h)
		plain := stripTestANSI.ReplaceAllString(rendered, "")
		fmt.Fprintf(&output, "SIDEBAR FIXTURE %dx%d\n%s\n", size.w, size.h, plain)
		if len(strings.Split(rendered, "\n")) != size.h {
			t.Fatal("wrong sidebar height")
		}
		for _, line := range strings.Split(rendered, "\n") {
			if lipgloss.Width(line) != size.w {
				t.Fatal("sidebar overflow")
			}
		}
		last := -1
		for _, section := range []string{"Session", "Host", "Slurm", "Your jobs"} {
			idx := strings.Index(plain, section)
			if idx < 0 && size.h < 18 && section != "Session" && section != "Host" {
				continue // short columns drop Slurm first, then jobs
			}
			if idx <= last {
				t.Fatalf("missing or out-of-order section %s: %s", section, plain)
			}
			last = idx
		}
	}
	kit.SetASCII(true)
	ascii := stripTestANSI.ReplaceAllString(RenderSidebar(d, 44, 24), "")
	kit.SetASCII(false)
	if strings.ContainsAny(ascii, "█░▓·→╭╮╰╯│─…") {
		t.Fatalf("ASCII fallback has Unicode indicators:\n%s", ascii)
	}
	fmt.Fprintf(&output, "SIDEBAR ASCII\n%s\n", ascii)
	d.Env.Slurm = false
	d.ClusterOK = false
	bare := stripTestANSI.ReplaceAllString(RenderSidebar(d, 44, 18), "")
	if strings.Contains(bare, "Slurm") || strings.Contains(bare, "login") {
		t.Fatal("host assumes cluster login node")
	}
	fmt.Fprintf(&output, "SIDEBAR STANDALONE HOST\n%s\n", bare)
	if path := os.Getenv("DEEPTHOUGHT_FIXTURE_PATH"); path != "" {
		if err := os.WriteFile(path, []byte(output.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestSidebarUnknownAndEmptyJobs(t *testing.T) {
	d := visualFixture()
	d.Cluster.YourJobs = nil
	d.Cluster.JobsRunning = 0
	d.Cluster.JobsPending = 0
	plain := stripTestANSI.ReplaceAllString(RenderSidebar(d, 44, 24), "")
	if !strings.Contains(plain, "Your jobs") || !strings.Contains(plain, "None") {
		t.Fatalf("empty jobs card should say None:\n%s", plain)
	}
	d.Cluster.JobsKnown = false
	d.Cluster.MemoryAllocKnown = false
	d.Cluster.GPUAllocKnown = false
	d.Env.Observation.CollectedAt = d.Clock.Add(-time.Hour)
	plain = stripTestANSI.ReplaceAllString(RenderSidebar(d, 44, 24), "")
	if !strings.Contains(plain, "unavailable") || !strings.Contains(plain, "stale") || !strings.Contains(plain, "GPU  --") {
		t.Fatal("unknown/stale states hidden", plain)
	}
}

// Cards fit exactly, shrink in priority order, and cap long job lists with
// "…and N more" instead of silently cutting rows.
func TestSidebarCardsFitAndOverflow(t *testing.T) {
	d := visualFixture()
	d.Model, d.Effort, d.Mode = "Qwen 3.5", "medium", "safe"
	d.LastContext, d.ContextWindow = 42000, 100000
	d.Env.Server, d.Env.Cwd = "server alice @ dt.example", "/scratch/alice/project"
	for i := 0; i < 20; i++ {
		d.Cluster.YourJobs = append(d.Cluster.YourJobs, slurm.Job{ID: fmt.Sprint(200 + i), Name: "sweep", State: "PENDING", Reason: "Priority"})
	}
	plain := stripTestANSI.ReplaceAllString(RenderSidebar(d, 44, 30), "")
	for _, want := range []string{"Qwen 3.5", "42%", "Connected · alice @ dt.example", "and ", " more", "/scratch/alice/project"} {
		if !strings.Contains(plain, want) {
			t.Errorf("sidebar missing %q:\n%s", want, plain)
		}
	}
	d.Alerts = []string{"Settings sync needs attention"}
	for _, h := range []int{8, 12, 20, 40} {
		v := RenderSidebar(d, 44, h)
		lines := strings.Split(v, "\n")
		if len(lines) != h {
			t.Fatalf("h=%d: %d rows", h, len(lines))
		}
		plain := stripTestANSI.ReplaceAllString(v, "")
		if !strings.Contains(plain, "Session") || !strings.Contains(plain, "sync needs attention") {
			t.Fatalf("h=%d: session and alerts must survive shrinking:\n%s", h, plain)
		}
	}
}
