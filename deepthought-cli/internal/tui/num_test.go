package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestFixedWidthNumerals(t *testing.T) {
	want := len(fixedTokens(0, false))
	for _, n := range []int{0, 7, 42, 999, 1000, 9999, 10000, 123456, 999999, 2300000} {
		for _, est := range []bool{false, true} {
			if got := fixedTokens(n, est); len(got) != want {
				t.Fatalf("fixedTokens(%d,%v)=%q width %d, want %d", n, est, got, len(got), want)
			}
		}
	}
	for _, d := range []time.Duration{0, 5 * time.Second, 59 * time.Second, 61 * time.Second, 59 * time.Minute, 3 * time.Hour} {
		if got := formatElapsed(d); len(got) != 6 {
			t.Fatalf("formatElapsed(%s)=%q is not 6 cells", d, got)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(fixedTokens(1200, true)), "~") {
		t.Fatal("estimates must carry a ~ prefix")
	}
}

func TestActivityLineDoesNotShiftWhileTicking(t *testing.T) {
	pos := -1
	for i, n := range []int{0, 9, 950, 1200, 15000, 250000} {
		line := ansi.Strip(RenderActivity(120, ActivityState{Busy: true, Verb: "Thinking", Tokens: n, Estimated: true, Elapsed: time.Duration(i*37) * time.Second}))
		p := strings.Index(line, "esc to interrupt")
		if p < 0 {
			t.Fatalf("missing interrupt hint: %q", line)
		}
		if pos >= 0 && p != pos {
			t.Fatalf("interrupt hint moved from %d to %d at tokens=%d", pos, p, n)
		}
		pos = p
	}
}

func TestContextMeterPairsBarWithPercent(t *testing.T) {
	got := ansi.Strip(ContextMeter(42000, 100000, false, 10))
	if !strings.Contains(got, "Context") || !strings.Contains(got, "42%") || !strings.Contains(got, "▓") {
		t.Fatalf("meter must show label, bar and exact percent: %q", got)
	}
	if est := ansi.Strip(ContextMeter(42000, 100000, true, 10)); !strings.Contains(est, "~ 42%") {
		t.Fatalf("estimated meter must be marked: %q", est)
	}
}

func TestStatusClockTickKeepsScroll(t *testing.T) {
	m := NewStatusModel(StatusInputs{Store: &fakeStore{}, Env: EnvInfo{Host: "login1", User: "u"}})
	m = m.Resize(100, 12)
	m.vp.SetYOffset(3)
	if m.vp.YOffset() != 3 {
		t.Skip("page shorter than the viewport; nothing to scroll")
	}
	for i := 0; i < 5; i++ {
		m = m.SetClock(time.Unix(int64(1_790_000_000+i), 0))
		_ = m.View()
	}
	if m.vp.YOffset() != 3 {
		t.Fatalf("clock ticks reset the scroll to %d", m.vp.YOffset())
	}
	m = m.SetSession(100, 50, 100, 1, 2)
	if m.vp.YOffset() != 3 {
		t.Fatalf("a data refresh reset the scroll to %d", m.vp.YOffset())
	}
}
