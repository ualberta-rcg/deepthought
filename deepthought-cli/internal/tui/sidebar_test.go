package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The sidebar renders its sections inside the column width and clips anything
// wider; no cluster → the (cluster n/a) degrade.
func TestSidebarRender(t *testing.T) {
	d := SidebarData{
		Cluster:       testSnapshot(),
		ClusterOK:     true,
		LastContext:   128000,
		ContextWindow: 310000,
		Providers: []ProviderRow{
			{Name: "vulcan-ks", Wire: "openai", KeySet: true, State: "ok"},
			{Name: "us-gw", Wire: "anthropic", State: "degraded"},
		},
	}
	v := RenderSidebar(d, SidebarWidth, 30)
	plain := stripTestANSI.ReplaceAllString(v, "")
	for _, want := range []string{"Cluster", "Your jobs", "Context", "Providers", "ok", "degraded", "310k"} {
		if !strings.Contains(plain, want) {
			t.Errorf("sidebar missing %q", want)
		}
	}
	for i, ln := range strings.Split(v, "\n") {
		if w := lipgloss.Width(ln); w > SidebarWidth+1 {
			t.Errorf("line %d width %d > sidebar width", i, w)
		}
	}

	na := RenderSidebar(SidebarData{}, SidebarWidth, 30)
	if !strings.Contains(stripTestANSI.ReplaceAllString(na, ""), "(cluster n/a)") {
		t.Error("no-cluster degrade missing")
	}
}

// The env brief: detected facts, capped hard at envBriefMax lines, "" when
// nothing detected.
func TestEnvBrief(t *testing.T) {
	m := ChatModel{env: EnvInfo{Host: "login01", Slurm: true, Module: true}}
	m = m.SetCluster(testSnapshot())
	b := m.envBrief()
	for _, want := range []string{"login01", "lmod modules", "l40s", "fairshare"} {
		if !strings.Contains(b, want) {
			t.Errorf("envBrief missing %q:\n%s", want, b)
		}
	}
	if n := strings.Count(strings.TrimSpace(b), "\n"); n > envBriefMax {
		t.Errorf("envBrief has %d lines, cap %d", n, envBriefMax)
	}

	empty := ChatModel{}.envBrief()
	if empty != "" {
		t.Errorf("undetected env should be empty, got %q", empty)
	}
}
