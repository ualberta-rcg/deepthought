package tui

import (
	"testing"

	"deepthought-cli/internal/babel"
)

// The thinking-level labels are plain (off→max), not themed.
func TestEffortLabelsNeutral(t *testing.T) {
	want := []struct {
		e babel.Effort
		s string
	}{
		{babel.EffortOff, "Off"},
		{babel.EffortLow, "Low"},
		{babel.EffortMedium, "Medium"},
		{babel.EffortHigh, "High"},
		{babel.EffortMax, "Max"},
	}
	for _, w := range want {
		if got := EffortLabel(w.e); got != w.s {
			t.Errorf("EffortLabel(%q) = %q, want %q", w.e, got, w.s)
		}
	}
}
