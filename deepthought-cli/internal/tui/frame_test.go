package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestScreenTitle(t *testing.T) {
	if got := screenTitle("Status"); got != "DeepThought › Status" {
		t.Errorf("screenTitle(Status) = %q", got)
	}
}

func TestEmptyRow(t *testing.T) {
	got := emptyRow("saved chats")
	if !strings.Contains(got, "(no saved chats yet)") {
		t.Errorf("emptyRow = %q", got)
	}
}

func TestTruncatePad(t *testing.T) {
	if got := truncatePad("ab", 5); got != "ab   " {
		t.Errorf("pad: %q", got)
	}
	if got := truncatePad("abcdef", 4); got != "abc…" {
		t.Errorf("truncate: %q", got)
	}
	if got := truncatePad("exact", 5); got != "exact" {
		t.Errorf("exact: %q", got)
	}
}

// clipLine truncates ANSI-aware: escape sequences survive, visible cells cap
// at width.
func TestClipLine(t *testing.T) {
	plain := clipLine("hello world", 5)
	if plain != "hello" {
		t.Errorf("clipLine plain = %q", plain)
	}
	if got := clipLine("hi", 5); got != "hi" {
		t.Errorf("short line should pass through: %q", got)
	}
	styled := styleError.Render("overflowing styled text")
	got := clipLine(styled, 4)
	if lipgloss.Width(got) != 4 {
		t.Errorf("styled clip width = %d, want 4", lipgloss.Width(got))
	}
	if !strings.Contains(got, "\x1b[") {
		t.Error("styled clip lost its ANSI sequence")
	}
}

// padBlock clips overwide lines instead of letting the frame wrap.
func TestPadBlockClips(t *testing.T) {
	got := padBlock("short\n"+strings.Repeat("x", 40), 10, 2)
	for i, ln := range strings.Split(got, "\n") {
		if w := lipgloss.Width(ln); w != 10 {
			t.Errorf("line %d width %d, want 10", i, w)
		}
	}
}

// overlayCenter composites the child centered over the parent's canvas:
// parent-only rows pass through, child rows splice in at the centered column,
// and plain-string parents stay readable.
func TestOverlayCenter(t *testing.T) {
	parent := strings.Join([]string{
		"aaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbbbbbbbbbbbbb",
		"cccccccccccccccccccc",
		"dddddddddddddddddddd",
	}, "\n")
	child := strings.Join([]string{"XY", "ZW"}, "\n")
	got := overlayCenter(parent, child)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	// Child rows (2) centered vertically → rows 1-2; child width 2 centered in
	// 20 → column 9.
	if !strings.HasPrefix(lines[1], "bbbbbbbbb") || !strings.Contains(lines[1], "XY") {
		t.Errorf("row1 = %q", lines[1])
	}
	if !strings.Contains(lines[2], "ZW") {
		t.Errorf("row2 = %q", lines[2])
	}
	// Untouched rows survive verbatim.
	if lines[0] != "aaaaaaaaaaaaaaaaaaaa" || lines[3] != "dddddddddddddddddddd" {
		t.Errorf("parent rows disturbed: %q / %q", lines[0], lines[3])
	}
	// Every line stays the parent's width.
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w != 20 {
			t.Errorf("line %d width %d, want 20", i, w)
		}
	}
}

// overlayCenter with a styled parent keeps the parent's leading escape on
// child rows (colors flow) and the child's own ANSI intact.
func TestOverlayCenterANSIParent(t *testing.T) {
	parent := styleError.Render("parent row that is long enough")
	child := styleToolResult.Render("kid")
	got := overlayCenter(parent, child)
	if !strings.Contains(got, "kid") {
		t.Error("child content lost")
	}
	if !strings.Contains(got, "\x1b[") {
		t.Error("ANSI lost entirely")
	}
}
