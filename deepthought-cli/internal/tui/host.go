package tui

import "fmt"

func CompactHost(e EnvInfo, w int) string {
	text := "Host: " + orDefault(e.ShortName, e.Host)
	if e.Observation.Allocation.ID != "" {
		text += " · job " + e.Observation.Allocation.ID
	}
	if e.Observation.Stale() {
		text += " · observing/stale"
	}
	return clipLine(text+" · Ctrl+P menu", w)
}
func observationRows(e EnvInfo) []string {
	r := e.Observation
	if r.CollectedAt.IsZero() {
		return []string{kv("observations", "collecting in background")}
	}
	rows := []string{kv("host type", r.Kind), kv("observed", r.CollectedAt.Format("2006-01-02 15:04:05 MST")), kv("freshness", fmt.Sprintf("stale=%t", r.Stale())), kv("host CPUs", fmt.Sprint(r.CPUs)), kv("CPU cgroup quota", r.CPULimit), kv("memory cgroup limit", r.MemoryLimit)}
	if r.MemoryKnown {
		rows = append(rows, kv("host memory", fmt.Sprintf("%d/%d GiB used", r.MemoryUsed>>30, r.MemoryTotal>>30)))
	}
	if r.CPUKnown {
		rows = append(rows, kv("host CPU utilization", fmt.Sprintf("%.1f%%", r.CPUPercent)))
	}
	if r.GPU != "" {
		rows = append(rows, kv("GPU name, MiB total/used, utilization %", r.GPU))
	}
	if r.Allocation.ID != "" {
		rows = append(rows, kv("current allocation", r.Allocation.ID), kv("allocated CPUs", r.Allocation.CPUs), kv("allocated memory", r.Allocation.Memory), kv("allocated GPUs", r.Allocation.GPUs), kv("allocated nodes", r.Allocation.Nodes))
	}
	for _, s := range r.Services {
		rows = append(rows, kv(s.Kind, s.Availability+" ("+s.Source+")"))
	}
	for _, s := range r.Storage {
		rows = append(rows, kv("filesystem "+s.Path, fmt.Sprintf("%d/%d GiB used; host filesystem capacity, not personal quota", s.Used>>30, s.Total>>30)))
	}
	return rows
}
