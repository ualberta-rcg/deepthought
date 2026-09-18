package tui

import (
	"charm.land/bubbletea/v2"

	"annorax/internal/babel"
)

// effortLevels maps the Effort ladder to the HHGTTG-flavored labels shown in the
// picker. Order MUST match babel's off→max ladder so ←/→ cycles naturally.
var effortLevels = []struct {
	val   babel.Effort
	label string
}{
	{babel.EffortOff, "Autopilot"},
	{babel.EffortLow, "Common Sense"},
	{babel.EffortMedium, "Pondering"},
	{babel.EffortHigh, "Deep Thought"},
	{babel.EffortMax, "Infinite Improbability"},
}

// EffortLabel returns the display label for an effort value (used by the top bar
// / status too). Unknown values fall back to the raw string.
func EffortLabel(e babel.Effort) string {
	for _, lvl := range effortLevels {
		if lvl.val == e {
			return lvl.label
		}
	}
	return string(e)
}

// EffortChosenMsg carries the effort selected in the picker. The root applies
// it (SetEffort + status update) in its own Update.
type EffortChosenMsg struct{ E babel.Effort }

// NewEffortOverlay builds the effort picker. current pre-selects the live
// effort; choosing emits EffortChosenMsg for the root to apply.
func NewEffortOverlay(current babel.Effort) Overlay {
	items := make([]overlayItem, len(effortLevels))
	sel := 0
	for i, e := range effortLevels {
		hint := ""
		if e.val == current {
			hint, sel = "· current", i
		}
		items[i] = overlayItem{label: e.label, hint: hint}
	}
	return choiceOverlay{
		title: "Effort",
		items: items,
		sel:   sel,
		commit: func(i int) tea.Cmd {
			if i < 0 || i >= len(effortLevels) {
				return nil
			}
			e := effortLevels[i].val
			return func() tea.Msg { return EffortChosenMsg{E: e} }
		},
	}
}
