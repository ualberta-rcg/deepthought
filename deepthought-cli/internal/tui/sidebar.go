package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/slurm"
)

// SidebarWidth is the live info column's width on the chat screen.
const SidebarWidth = 44

// RenderSidebarGutter paints the 1-column divider between the chat and the
// live column, from the palette (a retheme touches only styles.go).
func RenderSidebarGutter(h int) string {
	if h < 1 {
		return ""
	}
	// h LINES of one column each — the old horizontal repeat ("│"×h on one
	// row) made the join wider than the terminal on every line, wrapping the
	// screen and collapsing the sidebar to a sliver.
	return lipgloss.NewStyle().Foreground(colBarBg).Render(strings.TrimSuffix(strings.Repeat("│\n", h), "\n"))
}

// SidebarData is everything the sidebar renders; the root caches it from the
// existing ticks (clock, cluster poll, session usage) — no new timers.
type SidebarData struct {
	Clock         time.Time
	Cluster       slurm.ClusterSnapshot
	ClusterOK     bool    // Slurm snapshot gathered?
	Env           EnvInfo // host descriptor (always rendered)
	SessionIn     int
	SessionOut    int
	LastContext   int
	ContextWindow int
	Providers     []ProviderRow
	Skills        []string // discovered skill pack names
}

// RenderSidebar paints the compact live column: cluster one-liner + GPU bar,
// your jobs, the context meter, provider chips, and a dim "F12 for detail"
// footer. Everything is clipped to w — the padBlock safety net keeps the
// column an exact rectangle.
func RenderSidebar(d SidebarData, w, h int) string {
	if w < 10 || h < 1 {
		return ""
	}
	var secs []string

	// » Host — always (the descriptor's compact render).
	e := d.Env
	hostRows := []string{clipLine(" "+orDefault(e.ShortName, e.Host), w)}
	if e.User != "" {
		hostRows = append(hostRows, clipLine(" user "+e.User, w))
	}
	if e.OSName != "" {
		hostRows = append(hostRows, clipLine(" "+e.OSName, w))
	}
	if e.Kernel != "" {
		spec := e.Kernel
		if e.Arch != "" {
			spec += " " + e.Arch
		}
		if e.CPUs > 0 {
			spec += fmt.Sprintf(" · %dc", e.CPUs)
		}
		hostRows = append(hostRows, clipLine(" "+spec, w))
	}
	r := e.Observation
	if !r.CollectedAt.IsZero() {
		hostRows = append(hostRows, clipLine(" "+r.Kind+" · "+r.CollectedAt.Format("15:04:05"), w))
		if r.Stale() {
			hostRows = append(hostRows, " stale observations")
		}
		if r.MemoryKnown {
			hostRows = append(hostRows, clipLine(fmt.Sprintf(" RAM %s %d/%d GiB", healthBar(float64(r.MemoryUsed)/float64(r.MemoryTotal), max(4, w-24)), r.MemoryUsed>>30, r.MemoryTotal>>30), w))
		}
		if r.CPUKnown {
			hostRows = append(hostRows, clipLine(fmt.Sprintf(" CPU %s %.0f%% host", healthBar(r.CPUPercent/100, max(4, w-20)), r.CPUPercent), w))
		}
		if r.Allocation.ID != "" {
			hostRows = append(hostRows, clipLine(" job "+r.Allocation.ID+" · "+r.Allocation.CPUs+" CPUs", w), clipLine(" allocation "+r.Allocation.Memory, w))
		}
	}
	secs = append(secs, Section{Title: "Host", Rows: hostRows}.Render()...)
	if len(r.Services) > 0 {
		rows := []string{}
		for _, s := range r.Services {
			if s.Availability != "not detected" {
				rows = append(rows, clipLine(" "+s.Kind+" · "+s.Availability, w))
			}
		}
		secs = append(secs, Section{Title: "Services", Rows: rows}.Render()...)
	}

	// » Cluster — live when polled.
	if d.ClusterOK && d.Cluster.GPUs > 0 {
		frac := frac01(float64(d.Cluster.GPUsUsed), float64(d.Cluster.GPUs))
		gpu := fmt.Sprintf(" GPU allocation %s %d%%", healthBar(frac, max(4, w-25)), fracPct(frac))
		rows := []string{clipLine(gpu, w)}
		if typ := d.Cluster.GPUType; typ != "" {
			rows = append(rows, clipLine(fmt.Sprintf(" %s · %d run", typ, d.Cluster.JobsRunning), w))
		}
		secs = append(secs, Section{
			Title: "Cluster",
			Extra: fmt.Sprintf("%d run", d.Cluster.JobsRunning),
			Rows:  rows,
		}.Render()...)
	}

	// » Fairshare — when Slurm reports rows.
	if len(d.Cluster.FairshareRows) > 0 {
		rows := []string{}
		for _, r := range d.Cluster.FairshareRows {
			label, p := slurm.FairshareTier(r.Fairshare)
			col, bold := tierColor(label)
			rows = append(rows, clipLine(fmt.Sprintf(" %s %s %s %s",
				truncatePad(r.Account, 10),
				barFill(p, 10, col, bold),
				lipgloss.NewStyle().Foreground(col).Bold(bold).Render(truncatePad(label, 9)),
				fmt.Sprintf("%.2f", r.Fairshare)), w))
		}
		rows = append(rows, clipLine(" scheduling factor, not queue position", w))
		secs = append(secs, Section{Title: "Fairshare", Rows: rows}.Render()...)
	}

	// » Your jobs
	nr, np := 0, 0
	for _, j := range d.Cluster.YourJobs {
		switch j.State {
		case "RUNNING":
			nr++
		case "PENDING":
			np++
		}
	}
	jobsRow := fmt.Sprintf(" %d running · %d pending", nr, np)
	if !d.Cluster.JobsKnown {
		jobsRow = " job query unavailable"
	}
	if e.Slurm || d.ClusterOK {
		rows := []string{clipLine(jobsRow, w)}
		if d.Cluster.Err != nil {
			rows = append(rows, " scheduler unavailable / stale")
		}
		for i, j := range d.Cluster.YourJobs {
			if i >= 3 {
				break
			}
			rows = append(rows, clipLine(" "+j.ID+" "+j.Name+" "+j.State+" "+j.Elapsed, w))
			if j.State == "PENDING" {
				rows = append(rows, clipLine(" "+j.Reason, w))
			}
		}
		secs = append(secs, Section{Title: "Your jobs", Rows: rows}.Render()...)
	}

	// » Filesystem capacity — one compact line per filesystem when known.
	if len(d.Cluster.StorageRows) > 0 {
		rows := []string{}
		for _, r := range d.Cluster.StorageRows {
			rows = append(rows, clipLine(fmt.Sprintf(" %s %s/%s %d%%", truncatePad(r.Label, 8), r.Used, r.Size, r.Pct), w))
		}
		secs = append(secs, Section{Title: "Filesystem capacity", Rows: rows}.Render()...)
	}

	// » Context
	ctx := "  " + contextMeter(d.LastContext, d.ContextWindow)
	secs = append(secs, Section{Title: "Context", Rows: []string{clipLine(ctx, w)}}.Render()...)

	// » Providers
	prow := "  "
	for i, p := range d.Providers {
		if i > 0 {
			prow += " "
		}
		prow += p.Name + " " + stateChip(p.State)
	}
	secs = append(secs, Section{Title: "Providers", Rows: []string{clipLine(prow, w)}}.Render()...)

	// » Skills — the discovered packs.
	if len(d.Skills) > 0 {
		rows := []string{}
		for _, name := range d.Skills {
			rows = append(rows, clipLine(" "+name, w))
		}
		secs = append(secs, Section{Title: "Skills", Extra: fmt.Sprintf("%d packs", len(d.Skills)), Rows: rows}.Render()...)
	}

	secs = append(secs, styleSettingsFoot.Render("  Ctrl+P → Status for detail"))
	// Exactly w wide (the caller budgets w + a 1-col gutter — the old extra
	// PaddingLeft made every join one column wider than the terminal).
	return padBlock(strings.Join(secs, "\n"), w, h)
}
