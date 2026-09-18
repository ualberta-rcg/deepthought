package tui

import (
	"strings"
	"testing"
	"time"
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
