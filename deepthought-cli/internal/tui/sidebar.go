package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tui/kit"
)

const SidebarWidth = 44

func RenderSidebarGutter(h int) string {
	if h < 1 {
		return ""
	}
	return lipgloss.NewStyle().Foreground(colBarBg).Render(strings.TrimSuffix(strings.Repeat("│\n", h), "\n"))
}

// SidebarData is everything the sidebar draws. The root fills it from the
// background pollers; RenderSidebar never probes the host or Slurm itself.
type SidebarData struct {
	Clock                                             time.Time
	Cluster                                           slurm.ClusterSnapshot
	ClusterOK                                         bool
	Env                                               EnvInfo
	SessionIn, SessionOut, LastContext, ContextWindow int
	// ContextEstimated marks LastContext as a local estimate (shown with ~).
	ContextEstimated bool
	Providers        []ProviderRow
	Skills           []string
	// Session card.
	Model, Effort, Mode, ChatTitle string
	// Alerts are root-supplied conditions that need the user (sync, session).
	Alerts []string
}

func sidebarMeter(label string, value, total float64, known bool, suffix string, w int) string {
	if !known || total <= 0 || math.IsNaN(value) {
		return fmt.Sprintf("%-4s --", label)
	}
	g := kit.G()
	fraction := math.Max(0, math.Min(1, value/total))
	n := max(4, min(12, w-22))
	filled := int(math.Round(fraction * float64(n)))
	col := colSuccess
	if label == "" {
		col = colPrimary
	} else {
		if fraction >= .70 {
			col = colWarning
		}
		if fraction >= .90 {
			col = colDanger
		}
	}
	bar := lipgloss.NewStyle().Foreground(col).Render(strings.Repeat(g.BarFull, filled) + strings.Repeat(g.BarEmpty, n-filled))
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

// sidebarCard is one boxed section. more, when set, replaces the last visible
// row with "…and N more" when the card is cut short.
type sidebarCard struct {
	panel kit.Panel
	more  bool
	min   int // body rows the card needs to be worth showing
}

func (c sidebarCard) size() kit.Size {
	pref := len(c.panel.Body) + 2
	if c.panel.Footnote != "" {
		pref++
	}
	return kit.Size{Min: c.min + 2, Preferred: pref}
}

func (c sidebarCard) render(w, h int) string {
	p := c.panel
	if c.more {
		rows := h - 2
		if p.Footnote != "" && len(p.Body)+1 > rows {
			p.Footnote = ""
		}
		if len(p.Body) > rows && rows > 0 {
			hidden := len(p.Body) - rows + 1
			p.Body = append(append([]string(nil), p.Body[:rows-1]...), styleSystem.Render(fmt.Sprintf("%sand %d more", kit.G().Ellipsis, hidden)))
		}
	}
	return p.Render(w, h)
}

func (d SidebarData) sessionCard(inner int) sidebarCard {
	g := kit.G()
	var body []string
	if d.Model == "" {
		body = append(body, styleToolAsk.Render("No model")+styleSystem.Render(" · F1 Settings"))
	} else {
		parts := []string{d.Model}
		if d.Effort != "" {
			parts = append(parts, d.Effort)
		}
		if d.Mode != "" {
			parts = append(parts, d.Mode)
		}
		body = append(body, strings.Join(parts, " "+g.Bullet+" "))
	}
	body = append(body, ContextMeter(d.LastContext, d.ContextWindow, d.ContextEstimated, max(4, min(10, inner-15))))
	server := "Standalone"
	if s := d.Env.Server; s != "" && s != "standalone" {
		server = "Connected · " + strings.TrimPrefix(s, "server ")
	}
	body = append(body, styleSystem.Render(server))
	if d.ChatTitle != "" {
		body = append(body, d.ChatTitle)
	}
	if d.Env.Cwd != "" {
		body = append(body, styleSystem.Render(tailPath(d.Env.Cwd, inner)))
	}
	return sidebarCard{panel: kit.Panel{Title: "Session", Body: body}, min: 1}
}

// tailPath keeps the end of a path, which is the part that identifies it.
func tailPath(p string, w int) string {
	r := []rune(p)
	if len(r) <= w || w < 4 {
		return p
	}
	return kit.G().Ellipsis + string(r[len(r)-w+len([]rune(kit.G().Ellipsis)):])
}

func (d SidebarData) hostCard(inner int, now time.Time) sidebarCard {
	r := d.Env.Observation
	name := orDefault(r.Name, orDefault(d.Env.ShortName, orDefault(d.Env.Host, "local host")))
	var body []string
	if r.Allocation.ID != "" {
		body = append(body, "Job "+r.Allocation.ID+" · "+r.Allocation.CPUs+" CPUs · "+r.Allocation.Memory)
	}
	body = append(body,
		sidebarMeter("CPU", r.CPUPercent, 100, r.CPUKnown, fmt.Sprintf("%.0f%%", r.CPUPercent), inner),
		sidebarMeter("RAM", float64(r.MemoryUsed), float64(r.MemoryTotal), r.MemoryKnown, fmt.Sprintf("%d/%d GiB", r.MemoryUsed>>30, r.MemoryTotal>>30), inner),
	)
	if len(r.GPUs) > 0 {
		known, memoryKnown := true, true
		util, used, total := 0.0, 0.0, 0.0
		for _, g := range r.GPUs {
			known = known && g.UtilKnown
			memoryKnown = memoryKnown && g.MemoryKnown
			util += g.Utilization
			used += g.MemoryUsedMiB
			total += g.MemoryTotalMiB
		}
		util /= float64(len(r.GPUs))
		body = append(body,
			sidebarMeter("GPU", util, 100, known, fmt.Sprintf("%.0f%% · %d GPU", util, len(r.GPUs)), inner),
			sidebarMeter("VRAM", used, total, memoryKnown, fmt.Sprintf("%.0f/%.0f GiB", used/1024, total/1024), inner))
	}
	if len(r.Storage) > 0 {
		s := r.Storage[0]
		body = append(body, sidebarMeter("Disk", float64(s.Used), float64(s.Total), s.Total > 0, fmt.Sprintf("%.0f%% fs", 100*float64(s.Used)/float64(max(uint64(1), s.Total))), inner))
	}
	return sidebarCard{panel: kit.Panel{Title: "Host · " + name, Body: body, Footnote: "updated " + sidebarAge(r.CollectedAt, now, 90*time.Second)}, min: 2}
}

func (d SidebarData) clusterCard(inner int, now time.Time) sidebarCard {
	c := d.Cluster
	g := kit.G()
	queue := "Jobs --"
	if c.QueueKnown {
		total := c.ClusterRunning + c.ClusterPending
		if total == 0 {
			queue = "Jobs 0 running · 0 pending"
		} else {
			n := max(4, min(12, inner-22))
			running := int(math.Round(float64(c.ClusterRunning) / float64(total) * float64(n)))
			rchar, pchar := g.BarFull, g.BarEmpty
			if kit.ASCII() {
				rchar, pchar = "R", "P"
			}
			queue = "Jobs " + lipgloss.NewStyle().Foreground(colSuccess).Render(strings.Repeat(rchar, running)) + lipgloss.NewStyle().Foreground(colWarning).Render(strings.Repeat(pchar, n-running)) + fmt.Sprintf(" %d run / %d pend", c.ClusterRunning, c.ClusterPending)
		}
	}
	body := []string{queue,
		sidebarMeter("CPU", float64(c.CPUAlloc), float64(c.CPUTotal), c.CPUTotal > 0, fmt.Sprintf("%d/%d", c.CPUAlloc, c.CPUTotal), inner),
		sidebarMeter("GPU", float64(c.GPUsUsed), float64(c.GPUs), c.GPUAllocKnown, fmt.Sprintf("%d/%d", c.GPUsUsed, c.GPUs), inner),
		sidebarMeter("RAM", float64(c.MemAllocGB), float64(c.MemTotalGB), c.MemoryAllocKnown, fmt.Sprintf("%d/%d GiB", c.MemAllocGB, c.MemTotalGB), inner),
	}
	if len(c.FairshareRows) == 0 {
		body = append(body, "Fairshare unavailable")
	}
	for _, f := range c.FairshareRows {
		body = append(body, sidebarMeter("Fair", f.Fairshare, 1, true, fmt.Sprintf("%.2f %s", f.Fairshare, f.Account), inner))
	}
	foot := "updated " + sidebarAge(c.FetchedAt, now, 15*time.Minute)
	if c.Err != nil {
		foot = "Slurm unavailable"
	}
	return sidebarCard{panel: kit.Panel{Title: "Slurm · " + orDefault(c.ClusterName, "unknown cluster"), Body: body, Footnote: foot}, min: 1}
}

func (d SidebarData) jobsCard() sidebarCard {
	c := d.Cluster
	g := kit.G()
	p := kit.Panel{Title: "Your jobs"}
	switch {
	case !c.JobsKnown:
		p.Body = []string{"unavailable"}
	case len(c.YourJobs) == 0:
		p.Body = []string{styleSystem.Render("None")}
	default:
		p.Status = fmt.Sprintf("%d run · %d pend", c.JobsRunning, c.JobsPending)
		for _, j := range c.YourJobs {
			col, detail := colDim, j.Elapsed
			switch j.State {
			case "RUNNING":
				col = colSuccess
			case "PENDING":
				col, detail = colWarning, j.Reason
			}
			p.Body = append(p.Body, lipgloss.NewStyle().Foreground(col).Render(g.Dot)+" "+j.ID+" "+j.Name+" "+styleSystem.Render(strings.TrimSpace(strings.ToLower(j.State)+" "+detail)))
		}
	}
	return sidebarCard{panel: p, more: true, min: 1}
}

func (d SidebarData) alerts() []string {
	g := kit.G()
	var out []string
	for _, s := range d.Env.Observation.Storage {
		if s.Total > 0 && float64(s.Used)/float64(s.Total) >= .90 {
			out = append(out, strings.TrimSpace(fmt.Sprintf("Disk %.0f%% full %s", 100*float64(s.Used)/float64(s.Total), s.Path)))
		}
	}
	out = append(out, d.Alerts...)
	for i := range out {
		out[i] = styleToolAsk.Render(g.Warn) + " " + out[i]
	}
	return out
}

// RenderSidebar draws the boxed cards top to bottom: Session, Host, Slurm,
// Your jobs, then Alerts when there are any. When rows run short, cards
// shrink to their minimum and then drop in a fixed order (Slurm first, then
// jobs, then host); Session and Alerts stay.
func RenderSidebar(d SidebarData, w, h int) string {
	if w < 10 || h < 1 {
		return ""
	}
	now := d.Clock
	if now.IsZero() {
		now = time.Now()
	}
	inner := w - 2
	cards := []sidebarCard{d.sessionCard(inner), d.hostCard(inner, now)}
	slurmShown := d.Env.Slurm || d.ClusterOK
	if slurmShown {
		cards = append(cards, d.clusterCard(inner, now), d.jobsCard())
	}
	if a := d.alerts(); len(a) > 0 {
		cards = append(cards, sidebarCard{panel: kit.Panel{Title: "Alerts", Body: a}, more: true, min: 1})
	}
	// Priority (FitHeights order) differs from display order.
	prio := []int{0}
	if a := len(cards) - 1; a >= 0 && cards[a].panel.Title == "Alerts" {
		prio = append(prio, a)
	}
	prio = append(prio, 1)
	if slurmShown {
		prio = append(prio, 3, 2)
	}
	sizes := make([]kit.Size, len(prio))
	for i, idx := range prio {
		sizes[i] = cards[idx].size()
	}
	fit := kit.FitHeights(sizes, h)
	heights := make([]int, len(cards))
	for i, idx := range prio {
		heights[idx] = fit[i]
	}
	var lines []string
	for i, c := range cards {
		if heights[i] < 3 {
			continue
		}
		lines = append(lines, strings.Split(c.render(w, heights[i]), "\n")...)
	}
	if len(lines) < h {
		lines = append(lines, styleSettingsFoot.Render(" Ctrl+P commands"))
	}
	if kit.ASCII() {
		for i := range lines {
			lines[i] = strings.NewReplacer("·", "|", "→", ">", "█", "#", "░", ".", "▓", "#").Replace(lines[i])
		}
	}
	return padBlock(strings.Join(lines, "\n"), w, h)
}
