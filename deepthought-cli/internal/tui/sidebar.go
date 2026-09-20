package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/slurm"
)

// SidebarWidth is the live info column's width on the chat screen.
const SidebarWidth = 36

// RenderSidebarGutter paints the 1-column divider between the chat and the
// live column, from the palette (a retheme touches only styles.go).
func RenderSidebarGutter(h int) string {
	if h < 1 {
		return ""
	}
	return lipgloss.NewStyle().Foreground(colBarBg).Render(strings.Repeat("│", h))
}

// SidebarData is everything the sidebar renders; the root caches it from the
// existing ticks (clock, cluster poll, session usage) — no new timers.
type SidebarData struct {
	Clock         time.Time
	Cluster       slurm.ClusterSnapshot
	ClusterOK     bool    // false → render the Host section instead
	Env           EnvInfo // host facts for non-Slurm hosts
	SessionIn     int
	SessionOut    int
	LastContext   int
	ContextWindow int
	Providers     []ProviderRow
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

	// » Cluster (live when polled) or » Host (no Slurm here)
	if d.ClusterOK && d.Cluster.GPUs > 0 {
		frac := frac01(float64(d.Cluster.GPUsUsed), float64(d.Cluster.GPUs))
		gpu := fmt.Sprintf(" gpus  %s %d%%", healthBar(frac, w-14), fracPct(frac))
		secs = append(secs, Section{
			Title: "Cluster",
			Extra: fmt.Sprintf("%d run", d.Cluster.JobsRunning),
			Rows:  []string{clipLine(gpu, w)},
		}.Render()...)
	} else if e := d.Env; e.OSName != "" || e.Kernel != "" {
		secs = append(secs, Section{
			Title: "Host",
			Rows:  []string{clipLine(" "+orDefault(e.OSName, e.ShortName), w)},
		}.Render()...)
	} else {
		secs = append(secs, Section{
			Title: "Cluster",
			Rows:  []string{dimNote("  (cluster n/a)")},
		}.Render()...)
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
	jobsRow := fmt.Sprintf("  %d running · %d pending", nr, np)
	secs = append(secs, Section{Title: "Your jobs", Rows: []string{clipLine(jobsRow, w)}}.Render()...)

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

	secs = append(secs, styleSettingsFoot.Render("  F12 for detail"))
	// Exactly w wide (the caller budgets w + a 1-col gutter — the old extra
	// PaddingLeft made every join one column wider than the terminal).
	return padBlock(strings.Join(secs, "\n"), w, h)
}
