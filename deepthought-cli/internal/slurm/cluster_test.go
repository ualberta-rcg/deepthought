package slurm

import (
	"context"
	"testing"
)

func TestFairshareTier(t *testing.T) {
	cases := []struct {
		factor  float64
		wantLab string
		wantPct int
	}{
		{0.95, "boosted", 95},
		{0.80, "boosted", 80}, // boundary inclusive
		{0.65, "ahead", 65},
		{0.60, "ahead", 60}, // boundary inclusive
		{0.45, "nominal", 45},
		{0.40, "nominal", 40}, // boundary inclusive
		{0.25, "behind", 25},
		{0.20, "behind", 20}, // boundary inclusive
		{0.10, "throttled", 10},
		{0.0, "throttled", 0},
		{1.5, "boosted", 100},  // clamped to 1
		{-0.2, "throttled", 0}, // clamped to 0
	}
	for _, c := range cases {
		lab, pct := FairshareTier(c.factor)
		if lab != c.wantLab || pct != c.wantPct {
			t.Errorf("FairshareTier(%v) = (%q,%d), want (%q,%d)",
				c.factor, lab, pct, c.wantLab, c.wantPct)
		}
	}
}

func TestLevelFSTier(t *testing.T) {
	cases := map[string]string{
		"inf":     "good",
		"2.00":    "good",
		"1.25":    "nominal", // exactly LFSHigh is not "good"
		"1.00":    "nominal",
		"0.75":    "nominal", // exactly LFSLow is not "bad"
		"0.50":    "bad",
		"":        "",
		"garbage": "",
	}
	for in, want := range cases {
		if got := LevelFSTier(in); got != want {
			t.Errorf("LevelFSTier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGresGPUAndCPU(t *testing.T) {
	if got := gresGPU("gres/gpu=4,cpu=64"); got != 4 {
		t.Errorf("gresGPU gres+cpu = %d, want 4", got)
	}
	if got := gresGPU("cpu=64"); got != 0 {
		t.Errorf("gresGPU cpu-only = %d, want 0", got)
	}
	if got := gresGPU("gpu=4"); got != 0 {
		t.Errorf("gresGPU bare gpu (no gres/ prefix) = %d, want 0", got)
	}
	if got := cpuFromTRES("gres/gpu=4,cpu=64"); got != 64 {
		t.Errorf("cpuFromTRES gres+cpu = %d, want 64", got)
	}
	if got := cpuFromTRES("gres/gpu=4"); got != 0 {
		t.Errorf("cpuFromTRES gres-only = %d, want 0", got)
	}
	if got := cpuFromTRES(""); got != 0 {
		t.Errorf("cpuFromTRES empty = %d, want 0", got)
	}
}

// node1: 2 usable (cpu/mem/gpu all allow 2).
// node2: 0 usable (GPUs free but CPU fully allocated).
// node3: 4 usable (entirely free).
const gpuUsableFixture = `
NodeName=node1 CfgTRES=cpu=64,mem=256000M,gres/gpu=4 AllocTRES=cpu=32,mem=100000M,gres/gpu=2 RealMemory=256000 AllocMem=100000
NodeName=node2 CfgTRES=cpu=8,mem=256000M,gres/gpu=4 AllocTRES=cpu=8,mem=0M,gres/gpu=0 RealMemory=256000 AllocMem=0
NodeName=node3 CfgTRES=cpu=64,mem=256000M,gres/gpu=4 AllocTRES=cpu=0,mem=0M,gres/gpu=0 RealMemory=256000 AllocMem=0
`

func TestGatherGPUUsable(t *testing.T) {
	runner := &fakeRunner{out: gpuUsableFixture}
	if got := gatherGPUUsable(context.Background(), runner); got != 6 {
		t.Fatalf("gatherGPUUsable = %d, want 6 (2 + 0 + 4)", got)
	}
	// A CPU-starved node with free GPUs contributes nothing.
	runner2 := &fakeRunner{out: "NodeName=n CfgTRES=cpu=4,gres/gpu=4 AllocTRES=cpu=4,gres/gpu=0 RealMemory=1000 AllocMem=0\n"}
	if got := gatherGPUUsable(context.Background(), runner2); got != 0 {
		t.Fatalf("cpu-starved gatherGPUUsable = %d, want 0", got)
	}
	// No GPUs at all -> 0.
	if got := gatherGPUUsable(context.Background(), &fakeRunner{out: "NodeName=n CfgTRES=cpu=8,gres/gpu=0 AllocTRES=cpu=0,gres/gpu=0\n"}); got != 0 {
		t.Fatalf("no-gpu gatherGPUUsable = %d, want 0", got)
	}
}

const fairshareFixture = `
|aip-rahimk|rahimk|0.67|1.00|
|cc.debug|rahimk|0.10|inf|
|aip-rahimk|someoneelse|0.99|2.00|
`

func TestGatherFairshareRows(t *testing.T) {
	runner := &fakeRunner{out: fairshareFixture}
	rows := gatherFairshareRows(context.Background(), runner, "rahimk")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (user filter): %+v", len(rows), rows)
	}
	if rows[0].Account != "aip-rahimk" || rows[0].Fairshare != 0.67 || rows[0].LevelFS != "1.00" {
		t.Errorf("row0 = %+v", rows[0])
	}
	if rows[1].Account != "cc.debug" || rows[1].LevelFS != "inf" {
		t.Errorf("row1 = %+v", rows[1])
	}
}

const storageFixture = `Filesystem             Size  Used Avail Use% Mounted on
/dev/sda1              50G   19G   31G  39% /home/rahimk
scratch-pool           5.0T 801G  4.2T  16% /scratch/rahimk
project-pool           5.0T  2.9T  2.1T  59% /project/aip-rahimk
`

func TestParseStorageRows(t *testing.T) {
	rows := parseStorageRows([]byte(storageFixture), []string{"home", "scratch", "aip-rahimk"})
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(rows), rows)
	}
	want := []StorageRow{
		{Label: "home", Used: "19G", Size: "50G", Pct: 39},
		{Label: "scratch", Used: "801G", Size: "5.0T", Pct: 16},
		{Label: "aip-rahimk", Used: "2.9T", Size: "5.0T", Pct: 59},
	}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
}
