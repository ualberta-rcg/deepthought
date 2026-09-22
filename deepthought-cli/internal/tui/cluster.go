package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/slurm"
)

// clusterBarW is the bar width for the Cluster sections (tuned for a ≥80-col
// terminal). These are the vulcan-status-style block renderers the Status page
// shows as sections (Cluster / Your jobs / Fairshare / Filesystem capacity), gated on
// Slurm being detected. They read the cached snapshot, so nothing blocks.
const clusterBarW = 18

// renderClusterBlock: overall cluster load — nodes/queue, CPUs, memory, GPUs
// (incl. avail·usable).
func renderClusterBlock(c slurm.ClusterSnapshot) []string {
	if c.Err != nil && c.NodesTotal == 0 {
		return Section{Title: "Cluster", Rows: []string{styleError.Render("  ✗ " + c.Err.Error())}}.Render()
	}
	total := c.NodesTotal
	up := c.NodesUp
	down := total - up
	nodeFrac, cpuF, memF, gpuF := 0.0, 0.0, 0.0, 0.0
	if total > 0 {
		nodeFrac = float64(up) / float64(total)
	}
	if c.CPUTotal > 0 {
		cpuF = frac01(float64(c.CPUAlloc), float64(c.CPUTotal))
	}
	if c.MemTotalGB > 0 {
		memF = frac01(float64(c.MemAllocGB), float64(c.MemTotalGB))
	}
	if c.GPUs > 0 {
		gpuF = frac01(float64(c.GPUsUsed), float64(c.GPUs))
	}
	nodesLine := fmt.Sprintf("  nodes  %s  %d/%d up", healthBar(nodeFrac, clusterBarW), up, total)
	if down > 0 {
		nodesLine += "  " + lipgloss.NewStyle().Foreground(barWarn).Render(fmt.Sprintf("(%d down)", down))
	}
	body := []string{
		nodesLine,
		fmt.Sprintf("  cpus   %s  %d%%  (%d/%d)", healthBar(cpuF, clusterBarW), fracPct(cpuF), c.CPUAlloc, c.CPUTotal),
	}
	if c.MemTotalGB > 0 && c.MemoryAllocKnown {
		body = append(body, fmt.Sprintf("  mem    %s  %d%%  (%d/%d GB)", healthBar(memF, clusterBarW), fracPct(memF), c.MemAllocGB, c.MemTotalGB))
	} else if c.MemTotalGB > 0 {
		body = append(body, fmt.Sprintf("  mem    allocation unavailable · %d GB capacity", c.MemTotalGB))
	}
	if c.GPUs > 0 {
		avail := c.GPUs - c.GPUsUsed
		typ := ""
		if c.GPUType != "" {
			typ = " " + c.GPUType
		}
		if c.GPUAllocKnown {
			body = append(body, fmt.Sprintf("  gpus   %s  %d%% allocated (%d/%d%s · %d unallocated)",
				healthBar(gpuF, clusterBarW), fracPct(gpuF), c.GPUsUsed, c.GPUs, typ, avail))
		} else {
			body = append(body, fmt.Sprintf("  gpus   allocation unavailable · %d%s capacity", c.GPUs, typ))
		}
	}
	queue := "Cluster queue unavailable"
	if c.QueueKnown {
		queue = fmt.Sprintf("%d running · %d pending", c.ClusterRunning, c.ClusterPending)
	}
	return Section{
		Title:  "Slurm · " + orDefault(c.ClusterName, "cluster name unavailable"),
		Extra:  queue,
		Rows:   body,
		Note:   "Cluster allocations, not measured utilization or a guarantee that a job can start.",
		Source: "sinfo · squeue (aggregate states only)",
	}.Render()
}

// renderJobsBlock: the user's own running/pending jobs, with hold reasons.
func renderJobsBlock(c slurm.ClusterSnapshot) []string {
	nr, np := 0, 0
	for _, j := range c.YourJobs {
		switch j.State {
		case "RUNNING":
			nr++
		case "PENDING":
			np++
		}
	}
	body := []string{}
	if len(c.YourJobs) == 0 {
		if c.JobsKnown {
			body = append(body, "  "+emptyRow("active jobs"))
		} else {
			body = append(body, "  Job query unavailable; retry later")
		}
	} else {
		for _, j := range c.YourJobs {
			stStyle := lipgloss.NewStyle().Foreground(barWarn)
			if j.State == "RUNNING" {
				stStyle = styleSettingsVal
			}
			body = append(body, fmt.Sprintf("  %s  %s  %s  %s",
				lipgloss.NewStyle().Bold(true).Render(truncatePad(j.ID, 8)),
				stStyle.Render(truncatePad(j.State, 10)),
				dimNote("up "+truncatePad(orDefault(j.Elapsed, "-"), 8)),
				j.Name))
			if j.State == "PENDING" && j.Reason != "" {
				body = append(body, dimNote("       held: "+j.Reason))
			}
		}
	}
	return Section{
		Title:  "Your jobs",
		Extra:  fmt.Sprintf("%d running · %d pending", nr, np),
		Rows:   body,
		Source: "squeue --me",
	}.Render()
}

// renderFairshareBlock: per-account fairshare standing + LevelFS.
func renderFairshareBlock(c slurm.ClusterSnapshot) []string {
	body := []string{}
	if len(c.FairshareRows) == 0 {
		body = append(body, "  "+emptyRow("fairshare data"))
		return Section{Title: "Fairshare", Rows: body}.Render()
	}
	for _, r := range c.FairshareRows {
		line := fmt.Sprintf("  %s  %s  %.2f",
			truncatePad(r.Account, 14),
			barFill(fracPct(frac01(r.Fairshare, 1)), 12, barOK, false), r.Fairshare)
		if r.LevelFS != "" {
			line += "  " + dimNote("LevelFS "+r.LevelFS)
		}
		body = append(body, line)
	}
	return Section{
		Title:  "Fairshare",
		Rows:   body,
		Note:   "Fairshare is one scheduling factor, not a queue position or predicted start time.",
		Source: "sshare",
	}.Render()
}

// renderStorageBlock: how full the user's directories are (home/scratch/projects).
func renderStorageBlock(c slurm.ClusterSnapshot) []string {
	if len(c.StorageRows) == 0 {
		return Section{Title: "Filesystem capacity", Rows: []string{"  " + emptyRow("storage data")}}.Render()
	}
	body := []string{}
	for _, r := range c.StorageRows {
		body = append(body, fmt.Sprintf("  %s  %s  %d%%  %s / %s",
			truncatePad(r.Label, 14), diskBar(frac01(float64(r.Pct), 100), clusterBarW), r.Pct, r.Used, r.Size))
	}
	return Section{
		Title:  "Filesystem capacity",
		Rows:   body,
		Note:   "Mount-wide capacity, not your quota. Scratch is not backed up; idle files rotate out.",
		Source: "df",
	}.Render()
}

// frac01 clamps num/den to a 0–1 fraction (0 when den is 0).
func frac01(num, den float64) float64 {
	if den <= 0 {
		return 0
	}
	f := num / den
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
