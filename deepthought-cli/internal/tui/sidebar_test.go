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
		if w := lipgloss.Width(ln); w != SidebarWidth {
			t.Errorf("line %d width %d, want exactly %d (join must fit the terminal)", i, w, SidebarWidth)
		}
	}

	// The bare degrade: no cluster data — Host renders with placeholders and
	// the cluster-only sections are absent (TestSidebarV2Sections covers that
	// split; here just confirm it renders without the cluster bar).
	na := RenderSidebar(SidebarData{}, SidebarWidth, 30)
	if plain := stripTestANSI.ReplaceAllString(na, ""); strings.Contains(plain, "gpus") {
		t.Errorf("bare sidebar should not render a cluster bar: %q", plain[:80])
	}
}

// The env brief: detected facts, capped hard at envBriefMax lines, "" when
// nothing detected.
func TestEnvBrief(t *testing.T) {
	m := ChatModel{env: EnvInfo{Host: "login01", Slurm: true, Module: true}}
	m = m.SetCluster(testSnapshot())
	b := m.envBrief()
	for _, want := range []string{"login01", "lmod modules", "l40s"} { // fairshare lives in the cluster blurb now
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

// The env brief now leads with the host line (short name + arch/cpus/mem) and
// OS/kernel; a configured proxy renders as a negative capability.
func TestEnvBriefHostFacts(t *testing.T) {
	m := ChatModel{env: EnvInfo{
		ShortName: "vulcan-login1", LongName: "vulcan-login1.example.ca",
		OSName: "Ubuntu 22.04.4 LTS", Kernel: "5.15.0-107-generic",
		Arch: "amd64", CPUs: 64, MemGB: 251,
	}}
	b := m.envBrief()
	for _, want := range []string{"vulcan-login1", "amd64, 64 cpus", "Ubuntu 22.04.4 LTS", "kernel 5.15.0-107-generic"} {
		if !strings.Contains(b, want) {
			t.Errorf("envBrief missing %q:\n%s", want, b)
		}
	}
	t.Setenv("https_proxy", "proxy.example.ca:8080")
	if b := m.envBrief(); !strings.Contains(b, "proxy.example.ca:8080") || !strings.Contains(b, "direct connections fail") {
		t.Errorf("proxy negative capability missing:\n%s", b)
	}
}

// The gutter is VERTICAL: exactly h lines of one column (the old horizontal
// repeat made every joined row wider than the terminal — the "one-line
// sidebar").
func TestSidebarGutterVertical(t *testing.T) {
	g := RenderSidebarGutter(20)
	lines := strings.Split(g, "\n")
	if len(lines) != 20 {
		t.Fatalf("gutter has %d lines, want 20", len(lines))
	}
	for i, ln := range lines {
		if lipgloss.Width(ln) != 1 {
			t.Errorf("gutter line %d width %d, want 1", i, lipgloss.Width(ln))
		}
	}
}

// Join-level regression: chat + gutter + sidebar is EXACTLY h lines, each
// exactly chatW+1+SidebarWidth — nothing wraps, nothing clips.
func TestSidebarJoinExactRectangle(t *testing.T) {
	const w, h = 160, 40
	chatW := w - SidebarWidth - 1
	chat := lipgloss.NewStyle().Width(chatW).Height(h).Render("chat body")
	joined := lipgloss.JoinHorizontal(lipgloss.Top,
		chat, RenderSidebarGutter(h),
		RenderSidebar(SidebarData{
			Env: EnvInfo{ShortName: "h1", OSName: "TestOS", Kernel: "1.2.3", Arch: "amd64", CPUs: 8},
		}, SidebarWidth, h))
	lines := strings.Split(joined, "\n")
	if len(lines) != h {
		t.Fatalf("join has %d lines, want %d", len(lines), h)
	}
	for i, ln := range lines {
		if got := lipgloss.Width(ln); got != w {
			t.Errorf("join line %d width %d, want exactly %d", i, got, w)
		}
	}
}

// Sidebar v2: host always, fairshare gated on rows, dirs, and skills sections.
func TestSidebarV2Sections(t *testing.T) {
	snap := testSnapshot()
	d := SidebarData{
		Cluster: snap, ClusterOK: true,
		Env:         EnvInfo{ShortName: "login1", OSName: "Ubuntu 22.04", Kernel: "5.15", Arch: "amd64", CPUs: 8},
		LastContext: 128000, ContextWindow: 310000,
		Providers: []ProviderRow{{Name: "ks", State: "ok"}},
		Skills:    []string{"alliance-slurm", "alliance-cvmfs"},
	}
	v := RenderSidebar(d, SidebarWidth, 60)
	plain := stripTestANSI.ReplaceAllString(v, "")
	for _, want := range []string{"Host", "login1", "Ubuntu 22.04", "Cluster", "Fairshare", "ahead", "Your jobs", "Filesystem capacity", "scratch", "Context", "Providers", "Skills", "alliance-slurm"} {
		if !strings.Contains(plain, want) {
			t.Errorf("sidebar v2 missing %q:\n%s", want, plain)
		}
	}
	// No Slurm, no skills: Host still renders; fairshare/dirs/skills absent.
	plain2 := stripTestANSI.ReplaceAllString(RenderSidebar(SidebarData{Env: d.Env}, SidebarWidth, 60), "")
	if !strings.Contains(plain2, "login1") {
		t.Error("bare sidebar lost the Host section")
	}
	for _, absent := range []string{"Fairshare", "Filesystem capacity", "Skills"} {
		if strings.Contains(plain2, absent) {
			t.Errorf("bare sidebar should not show %q", absent)
		}
	}
}
