package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func TestRenderTopBarHasGreyBandAndFHints(t *testing.T) {
	bar := RenderTopBar(120, time.Date(2026, 7, 28, 8, 18, 42, 0, time.Local), StatusInfo{
		Model: "qwen", Mode: "safe", Effort: "medium",
	})
	if !strings.Contains(bar, "48;5;238") {
		t.Fatalf("expected ANSI 238 background in top bar, got %q", truncate(bar, 200))
	}
	if !strings.Contains(bar, "[F3]") || !strings.Contains(bar, "[F4]") {
		t.Fatalf("missing F-key hints: %q", truncate(bar, 200))
	}
	bot := RenderBottomBar(80, "~ · qwen · safe")
	if !strings.Contains(bot, "48;5;238") {
		t.Fatalf("expected ANSI 238 background in bottom bar")
	}
}

func TestChatChromeHeightLegendToggle(t *testing.T) {
	if got := ChatChromeHeight(true); got != 3 {
		t.Errorf("ChatChromeHeight(true) = %d, want 3 (top+legend+bottom)", got)
	}
	if got := ChatChromeHeight(false); got != 2 {
		t.Errorf("ChatChromeHeight(false) = %d, want 2 (top+bottom)", got)
	}
}

func TestRenderKeyLegendRow(t *testing.T) {
	wide := RenderKeyLegendRow(200)
	// Wide: full labels present, on the solid band, every F-key accounted for.
	for _, want := range []string{"48;5;238", "F10", "cluster", "F11", "software", "F12", "status"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide legend missing %q: %q", want, truncate(wide, 200))
		}
	}
	if lipgloss.Width(wide) != 200 {
		t.Errorf("wide legend width = %d, want exactly 200", lipgloss.Width(wide))
	}

	// Narrow: drops labels (no "cluster" word) but keeps the F-key tokens, and
	// never overflows.
	narrow := RenderKeyLegendRow(44)
	if strings.Contains(narrow, "cluster") {
		t.Errorf("narrow legend should drop labels: %q", truncate(narrow, 120))
	}
	if !strings.Contains(narrow, "F10") {
		t.Errorf("narrow legend lost F10 token: %q", truncate(narrow, 120))
	}
	if lipgloss.Width(narrow) > 44 {
		t.Errorf("narrow legend overflowed: width %d > 44", lipgloss.Width(narrow))
	}

	// Zero width: empty, no crash.
	if got := RenderKeyLegendRow(0); got != "" {
		t.Errorf("RenderKeyLegendRow(0) = %q, want empty", got)
	}
}
