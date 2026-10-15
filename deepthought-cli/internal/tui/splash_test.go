package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var splashANSI = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func TestBrandGradientIsSmoothAndBounded(t *testing.T) {
	first := brandGradientHex(0)
	mid := brandGradientHex(0.5)
	last := brandGradientHex(1)
	if first != gradFrom {
		t.Fatalf("t=0 = %s, want %s", first, gradFrom)
	}
	if last != gradTo {
		t.Fatalf("t=1 = %s, want %s", last, gradTo)
	}
	if mid == first || mid == last {
		t.Fatalf("midpoint %s should sit between %s and %s", mid, first, last)
	}
	if brandGradientHex(-2) != first || brandGradientHex(3) != last {
		t.Fatal("out-of-range t not clamped")
	}
}

func TestWordmarkHeaderFits(t *testing.T) {
	wide := wordmarkHeader(120, 20)
	if lines := strings.Split(wide, "\n"); len(lines) < 3 {
		t.Fatalf("expected multi-line art at 120 cols, got %d lines", len(lines))
	}
	for _, w := range []int{120, 80, 60, 30} {
		h := wordmarkHeader(w, 20)
		for _, ln := range strings.Split(h, "\n") {
			if got := lipgloss.Width(ln); got > w {
				t.Errorf("width %d: line is %d cols wide", w, got)
			}
		}
	}
	tiny := splashANSI.ReplaceAllString(wordmarkHeader(30, 4), "")
	if !strings.Contains(tiny, "DeepThought") {
		t.Fatalf("tiny fallback = %q, want plain 'DeepThought'", tiny)
	}
}

func TestHomeScreenIdentity(t *testing.T) {
	m := NewSplashModel(BootInfo{
		Model:        "qwen",
		Provider:     "vulcan-ks",
		Ready:        true,
		Version:      "DeepThought v0.1.0",
		Host:         "vulcan1",
		SystemID:     "sys-vulcan1-1234",
		Cluster:      "vulcan (alliance)",
		Capabilities: "Slurm · Lmod · CVMFS",
		Cwd:          "/scratch/work",
	}, "abc")
	full := splashANSI.ReplaceAllString(m.Resize(120, 30).View(), "")
	for _, want := range []string{
		"DEEPTHOUGHT",
		"UNIVERSITY OF ALBERTA",
		"DeepThought v0.1.0",
		"qwen · vulcan-ks",
		"vulcan1",
		"/scratch/work",
		"SINGLE PLAYER",
		"Run standalone",
		"MULTIPLAYER",
		"Log in to server",
		"AI LOADOUT",
		"Set up AI providers",
		"COMMAND DECK",
		"SYSTEM CABINET",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("full home missing %q:\n%s", want, full)
		}
	}

	compact := splashANSI.ReplaceAllString(m.Resize(70, 20).View(), "")
	if !strings.Contains(compact, "DON'T PANIC") || !strings.Contains(compact, "DeepThought v0.1.0") || !strings.Contains(compact, "qwen · vulcan-ks") {
		t.Fatalf("compact home missing identity/model:\n%s", compact)
	}
	if strings.Contains(compact, "UNIVERSITY OF ALBERTA") || strings.Contains(compact, "/scratch/work") {
		t.Fatalf("compact home did not drop verbose lines:\n%s", compact)
	}
}
