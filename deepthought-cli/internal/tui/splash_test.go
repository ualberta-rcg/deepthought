package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var splashANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func TestBrandGradient(t *testing.T) {
	if got := brandGradientHex(0); got != gradFrom {
		t.Errorf("brandGradientHex(0) = %s, want %s", got, gradFrom)
	}
	if got := brandGradientHex(1); got != gradTo {
		t.Errorf("brandGradientHex(1) = %s, want %s", got, gradTo)
	}
	mid := brandGradientHex(0.5)
	if mid == gradFrom || mid == gradTo {
		t.Errorf("brandGradientHex(0.5) = %s, want a distinct midpoint", mid)
	}
	// Clamps must not panic and must pin to the endpoints.
	if got := brandGradientHex(-1); got != gradFrom {
		t.Errorf("brandGradientHex(-1) = %s, want %s", got, gradFrom)
	}
	if got := brandGradientHex(2); got != gradTo {
		t.Errorf("brandGradientHex(2) = %s, want %s", got, gradTo)
	}
}

// dontPanicHeader must always produce lines that fit the requested box, at
// every size from tiny to huge, and degrade to the plain wordmark when no art
// fits.
func TestDontPanicHeaderFits(t *testing.T) {
	for _, sz := range [][2]int{{200, 30}, {100, 20}, {80, 17}, {60, 10}, {40, 6}} {
		w, h := sz[0], sz[1]
		got := dontPanicHeader(w, h)
		if got == "" {
			t.Fatalf("dontPanicHeader(%d,%d) is empty", w, h)
		}
		for i, ln := range strings.Split(got, "\n") {
			if lw := lipgloss.Width(ln); lw > w {
				t.Errorf("size %dx%d: line %d width %d overflows", w, h, i, lw)
			}
		}
		if rows := len(strings.Split(got, "\n")); rows > h {
			t.Errorf("size %dx%d: header has %d rows, exceeds %d", w, h, rows, h)
		}
	}
	// At a size where no art fits, the plain wordmark carries the message.
	plain := splashANSI.ReplaceAllString(dontPanicHeader(30, 4), "")
	if !strings.Contains(plain, "DON'T PANIC") {
		t.Errorf("tiny-size fallback lost the wordmark: %q", plain)
	}
}

// The full splash view renders without panicking and carries the boot status.
func TestSplashViewRenders(t *testing.T) {
	m := NewSplashModel(BootInfo{Model: "qwen", Provider: "vulcan-ks", Ready: true}, "abc")
	m = m.Resize(100, 30)
	v := m.View()
	if v == "" {
		t.Fatal("splash view is empty")
	}
	if !strings.Contains(splashANSI.ReplaceAllString(v, ""), "qwen · vulcan-ks") {
		t.Error("splash view missing the boot status line")
	}
}
