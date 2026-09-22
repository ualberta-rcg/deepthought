package tui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"deepthought-cli/internal/slurm"
)

const SidebarWidth = 44

func RenderSidebarGutter(h int) string {
	if h < 1 {
		return ""
	}
	return lipgloss.NewStyle().Foreground(colBarBg).Render(strings.TrimSuffix(strings.Repeat("│\n", h), "\n"))
}

type SidebarData struct {
	Clock                                             time.Time
	Cluster                                           slurm.ClusterSnapshot
	ClusterOK                                         bool
	Env                                               EnvInfo
	SessionIn, SessionOut, LastContext, ContextWindow int
	Providers                                         []ProviderRow
	Skills                                            []string
	ASCII                                             bool
}
type sidebarBlock struct {
	title   string
	rows    []string
	minimum int
}

func sidebarMeter(label string, value, total float64, known bool, suffix string, w int, ascii bool) string {
	if !known || total <= 0 || math.IsNaN(value) {
		return fmt.Sprintf("%-4s --", label)
	}
	fraction := math.Max(0, math.Min(1, value/total))
	n := max(4, min(12, w-22))
	filled := int(math.Round(fraction * float64(n)))
	full, empty := "█", "░"
	if ascii {
		full, empty = "#", "-"
	}
	col := colSuccess
	if label == "" {
		col = colPrimary
	} else {
		if fraction >= .85 {
			col = colWarning
		}
		if fraction >= .95 {
			col = colDanger
		}
	}
	bar := lipgloss.NewStyle().Foreground(col).Render(strings.Repeat(full, filled) + strings.Repeat(empty, n-filled))
	return fmt.Sprintf("%-4s %s %s", label, bar, suffix)
}
func sidebarAge(t, now time.Time, limit time.Duration) string {
	if t.IsZero() {
		return "unknown"
	}
	age := now.Sub(t)
	if age < 0 {
		age = 0
	}
	if age > limit {
		return "stale"
	}
	if age < time.Minute {
		return fmt.Sprintf("%ds", int(age.Seconds()))
	}
	return fmt.Sprintf("%dm", int(age.Minutes()))
}
func RenderSidebar(d SidebarData, w, h int) string {
	if w < 10 || h < 1 {
		return ""
	}
	ascii := d.ASCII || os.Getenv("TERM") == "dumb"
	now := d.Clock
	if now.IsZero() {
		now = time.Now()
	}
	r, c := d.Env.Observation, d.Cluster
	name := orDefault(r.Name, orDefault(d.Env.ShortName, orDefault(d.Env.Host, "local host")))
	host := sidebarBlock{title: "Host · " + name, minimum: 2, rows: []string{
		sidebarMeter("CPU", r.CPUPercent, 100, r.CPUKnown, fmt.Sprintf("%.0f%%", r.CPUPercent), w, ascii),
		sidebarMeter("RAM", float64(r.MemoryUsed), float64(r.MemoryTotal), r.MemoryKnown, fmt.Sprintf("%d/%d GiB", r.MemoryUsed>>30, r.MemoryTotal>>30), w, ascii),
	}}
	if len(r.GPUs) > 0 {
		known := true
		util := 0.0
		used, total := 0.0, 0.0
		memoryKnown := true
		for _, g := range r.GPUs {
			known = known && g.UtilKnown
			memoryKnown = memoryKnown && g.MemoryKnown
			util += g.Utilization
			used += g.MemoryUsedMiB
			total += g.MemoryTotalMiB
		}
		util /= float64(len(r.GPUs))
		host.rows = append(host.rows, sidebarMeter("GPU", util, 100, known, fmt.Sprintf("%.0f%% · %d GPU", util, len(r.GPUs)), w, ascii), sidebarMeter("VRAM", used, total, memoryKnown, fmt.Sprintf("%.0f/%.0f GiB", used/1024, total/1024), w, ascii))
	}
	if r.Allocation.ID != "" {
		host.rows = append(host.rows, "Job "+r.Allocation.ID+" · "+r.Allocation.CPUs+" CPUs · "+r.Allocation.Memory)
	}
	if len(r.Storage) > 0 {
		s := r.Storage[0]
		host.rows = append(host.rows, sidebarMeter("Disk", float64(s.Used), float64(s.Total), s.Total > 0, fmt.Sprintf("%.0f%% fs", 100*float64(s.Used)/float64(max(uint64(1), s.Total))), w, ascii))
	}
	host.title += " · " + sidebarAge(r.CollectedAt, now, 90*time.Second)
	blocks := []sidebarBlock{host}
	if d.Env.Slurm || d.ClusterOK {
		title := "Slurm · " + clipLine(orDefault(c.ClusterName, "unknown cluster"), max(6, w-24)) + " · alloc"
		if c.Err != nil {
			title += " · unavailable"
		} else {
			title += " · " + sidebarAge(c.FetchedAt, now, 15*time.Minute)
		}
		queue := "Jobs --"
		if c.QueueKnown {
			total := c.ClusterRunning + c.ClusterPending
			if total == 0 {
				queue = "Jobs 0 running · 0 pending"
			} else {
				n := max(4, min(12, w-22))
				running := int(math.Round(float64(c.ClusterRunning) / float64(total) * float64(n)))
				rchar, pchar := "█", "░"
				if ascii {
					rchar, pchar = "R", "P"
				}
				queue = lipgloss.NewStyle().Foreground(colSuccess).Render(strings.Repeat(rchar, running)) + lipgloss.NewStyle().Foreground(colWarning).Render(strings.Repeat(pchar, n-running)) + fmt.Sprintf(" %d run / %d pend", c.ClusterRunning, c.ClusterPending)
			}
		}
		cluster := sidebarBlock{title: title, minimum: 1, rows: []string{queue,
			sidebarMeter("CPU", float64(c.CPUAlloc), float64(c.CPUTotal), c.CPUTotal > 0, fmt.Sprintf("%d/%d", c.CPUAlloc, c.CPUTotal), w, ascii),
			sidebarMeter("GPU", float64(c.GPUsUsed), float64(c.GPUs), c.GPUAllocKnown, fmt.Sprintf("%d/%d", c.GPUsUsed, c.GPUs), w, ascii),
			sidebarMeter("RAM", float64(c.MemAllocGB), float64(c.MemTotalGB), c.MemoryAllocKnown, fmt.Sprintf("%d/%d GiB", c.MemAllocGB, c.MemTotalGB), w, ascii),
		}}
		blocks = append(blocks, cluster)
		fair := sidebarBlock{title: "Your fairshare", minimum: 1, rows: []string{"unavailable"}}
		if len(c.FairshareRows) > 0 {
			fair.rows = nil
			for _, f := range c.FairshareRows {
				fair.rows = append(fair.rows, sidebarMeter("", f.Fairshare, 1, true, fmt.Sprintf("%.2f %s", f.Fairshare, f.Account), w, ascii))
			}
		}
		blocks = append(blocks, fair)
		if !c.JobsKnown || len(c.YourJobs) > 0 {
			jobs := sidebarBlock{title: "Your jobs", minimum: 1, rows: []string{fmt.Sprintf("%d running · %d pending", c.JobsRunning, c.JobsPending)}}
			if !c.JobsKnown {
				jobs.rows = []string{"unavailable"}
			}
			for _, j := range c.YourJobs {
				state := j.State
				detail := j.Elapsed
				if state == "PENDING" {
					detail = j.Reason
				}
				jobs.rows = append(jobs.rows, j.ID+" "+j.Name+" "+state+" "+detail)
			}
			blocks = append(blocks, jobs)
		}
	}
	// Reserve one summary per section before distributing optional details. Jobs
	// never disappear just because the host or cluster has many resource rows.
	counts := make([]int, len(blocks))
	remaining := h - 1
	for i, b := range blocks {
		if remaining < 2 {
			break
		}
		counts[i] = 1 + min(b.minimum, remaining-1)
		remaining -= counts[i]
	}
	// Cluster allocations, host detail, job rows, then additional accounts.
	order := []int{1, 0, 3, 2}
	for _, i := range order {
		if i >= len(blocks) || counts[i] == 0 {
			continue
		}
		extra := min(remaining, len(blocks[i].rows)+1-counts[i])
		counts[i] += extra
		remaining -= extra
	}
	var lines []string
	for i, b := range blocks {
		if counts[i] == 0 {
			continue
		}
		lines = append(lines, styleSettingsTitle.Render(clipLine(b.title, w)))
		for _, line := range b.rows[:min(len(b.rows), counts[i]-1)] {
			lines = append(lines, clipLine(line, w))
		}
	}
	if len(lines) < h {
		lines = append(lines, styleSettingsFoot.Render("Ctrl+P → Status"))
	}
	if ascii {
		for i := range lines {
			lines[i] = strings.NewReplacer("·", "|", "→", ">").Replace(lines[i])
		}
	}
	return padBlock(strings.Join(lines, "\n"), w, h)
}
