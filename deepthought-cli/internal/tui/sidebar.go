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
	secs = append(secs, Section{Title: "Host", Rows: hostRows}.Render()...)

	// » Cluster — live when polled.
	if d.ClusterOK && d.Cluster.GPUs > 0 {
		frac := frac01(float64(d.Cluster.GPUsUsed), float64(d.Cluster.GPUs))
		gpu := fmt.Sprintf(" gpus  %s %d%%", healthBar(frac, w-14), fracPct(frac))
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
	secs = append(secs, Section{Title: "Your jobs", Rows: []string{clipLine(jobsRow, w)}}.Render()...)

	// » Your dirs — one compact line per filesystem when known.
	if len(d.Cluster.StorageRows) > 0 {
		rows := []string{}
		for _, r := range d.Cluster.StorageRows {
			rows = append(rows, clipLine(fmt.Sprintf(" %s %s/%s %d%%", truncatePad(r.Label, 8), r.Used, r.Size, r.Pct), w))
		}
		secs = append(secs, Section{Title: "Your dirs", Rows: rows}.Render()...)
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

	secs = append(secs, styleSettingsFoot.Render("  F12 for detail"))
	// Exactly w wide (the caller budgets w + a 1-col gutter — the old extra
	// PaddingLeft made every join one column wider than the terminal).
	return padBlock(strings.Join(secs, "\n"), w, h)
}
