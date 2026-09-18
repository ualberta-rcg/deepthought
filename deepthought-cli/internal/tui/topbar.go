package tui

import (
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/babel"
)

// TopBarHeight is the number of terminal rows the root reserves for the top bar.
// Exported because both the root model (package app) and the SSH PTY seed must
// subtract it from the chat region.
const TopBarHeight = 1

// BottomBarHeight is the number of terminal rows the root reserves for the
// bottom chrome band (status line). Sibling of TopBarHeight.
const BottomBarHeight = 1

// LegendRowHeight is the number of rows the optional F-key legend band under the
// top bar occupies (1 when shown, 0 when the Appearance toggle hides it).
const LegendRowHeight = 1

// ChatChromeHeight is the number of rows the root subtracts from the terminal
// height before handing the remainder to ChatModel: top bar + (optional legend
// row) + bottom bar. Pass the live legend setting so hiding it reclaims a row.
func ChatChromeHeight(legend bool) int {
	rows := TopBarHeight + BottomBarHeight
	if legend {
		rows += LegendRowHeight
	}
	return rows
}

// TickMsg carries the wall-clock time produced by TickClock. The receiver re-arms
// it on every tick (tea.Every fires once).
type TickMsg time.Time

// TickClock arms the next 1 Hz wall-clock tick. tea.Every is aligned to the system
// clock, so HH:MM:SS rolls over crisply. Return TickClock() again from the TickMsg
// handler to keep it ticking.
func TickClock() tea.Cmd {
	return tea.Every(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// RenderTopBar paints the 1-row dark-grey top bar: rainbow DeepThought · model [F3]
// · mode · effort [F4] on the left, a responsive clock on the right. The band
// background is applied to every cell so the bar is solid dark grey, not black.
func RenderTopBar(w int, clock time.Time, st StatusInfo) string {
	if w < 1 {
		return ""
	}
	drift := clock.Second() % len(markBands)
	left := lipgloss.JoinHorizontal(lipgloss.Left,
		rainbowWord("DeepThought", drift),
		styleStatusSep.Render(" │ "),
		styleStatusModel.Render(st.Model),
		styleStatusHint.Render(" [F3]"),
		styleStatusSep.Render(" │ "),
		styleStatusMode.Render(orDefault(st.Mode, "—")),
	)
	// Effort is the reasoning dial (off = no thinking); show it as its own chip
	// with the HHGTTG label so it's glance-worthy, plus its F-key hint.
	if st.Effort != "" {
		left = lipgloss.JoinHorizontal(lipgloss.Left,
			left,
			styleStatusSep.Render(" │ "),
			styleStatusComp.Render(EffortLabel(babel.Effort(st.Effort))),
			styleStatusHint.Render(" [F4]"),
		)
	}
	clockStr := styleClock.Render(formatBarClock(clock, w, lipgloss.Width(left)))
	return padBar(w, left, clockStr)
}

// fKeyLegend is the (key, label) set shown in the top-bar legend row, in F-key
// order. Single source of truth so the row (and any future legend surface)
// stays in sync with the actual bindings.
var fKeyLegend = []struct{ key, label string }{
	{"F1", "help"}, {"F2", "settings"}, {"F3", "model"}, {"F4", "effort"},
	{"F5", "new"}, {"F6", "resume"}, {"F7", "grid"}, {"F8", "stats"},
	{"F9", "mode"}, {"F10", "cluster"}, {"F11", "software"}, {"F12", "status"},
}

// RenderKeyLegendRow paints the 1-row F-key legend on the same solid band as the
// top bar: " F1 help   F2 settings   …  F12 status ". It degrades gracefully as
// the terminal narrows — first dropping the labels (keys only), then truncating —
// so it never wraps or overflows the row.
func RenderKeyLegendRow(w int) string {
	if w < 1 {
		return ""
	}
	if full := legendCells(true); lipgloss.Width(full) <= w {
		return padBar(w, full, "")
	}
	if keys := legendCells(false); lipgloss.Width(keys) <= w {
		return padBar(w, keys, "")
	}
	return styleBarPad.Width(w).MaxWidth(w).Render(legendCells(false))
}

// legendCells joins the F-key chips; withLabel renders "F1 help", without just
// "F1" for narrow terminals.
func legendCells(withLabel bool) string {
	parts := make([]string, 0, len(fKeyLegend))
	for _, e := range fKeyLegend {
		if withLabel {
			parts = append(parts, styleBarKey.Render(e.key)+styleStatusHint.Render(" "+e.label))
		} else {
			parts = append(parts, styleBarKey.Render(e.key))
		}
	}
	return strings.Join(parts, styleStatusHint.Render("   "))
}

// RenderBottomBar paints the 1-row dark-grey bottom chrome band. content is
// optional (empty → blank band); when set it is left-aligned on the band with
// bar foreground so it doesn't sit as unstyled black-on-black text.
func RenderBottomBar(w int, content string) string {
	if w < 1 {
		return ""
	}
	left := content
	if strings.TrimSpace(left) == "" {
		left = styleBarPad.Render("")
	} else {
		left = styleBarText.Render(left)
	}
	return padBar(w, left, "")
}

// padBar joins left + right on a solid dark-grey band of width w, right-aligning
// the right piece when present. Truncates the left if the pair won't fit.
//
// Every cell (left, gap, right) carries Background(colBarBg) already — we do
// NOT wrap the joined string in styleTopBar.Width(w), which fights nested ANSI
// and can wash the band back to the terminal default.
func padBar(w int, left, right string) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	gap := w - lw - rw
	if gap < 0 {
		// Too narrow: drop the right piece, then truncate left if needed.
		if rw > 0 && w >= lw {
			right, rw = "", 0
			gap = w - lw
		} else {
			// Hard truncate to w cells while keeping the band background.
			return styleBarPad.Width(w).MaxWidth(w).Render(left)
		}
	}
	pad := styleBarPad.Render(strings.Repeat(" ", gap))
	return left + pad + right
}

// rainbowWord colors each rune of s with the splash spectrum, drifted by drift
// bands so the hue walks across the word on every clock second.
func rainbowWord(s string, drift int) string {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return ""
	}
	var b strings.Builder
	i := 0
	for _, r := range s {
		c := lipgloss.Color(bandForRow(i, drift, n))
		b.WriteString(lipgloss.NewStyle().
			Foreground(c).
			Background(colBarBg).
			Bold(true).
			Render(string(r)))
		i++
	}
	return b.String()
}

// formatBarClock picks a clock format that fits the remaining width:
//   - wide  (≥ 28 free cells): "Mon Jul 27 2026 · 20:18:42 MDT"
//   - medium (≥ 12): "20:18:42"
//   - narrow: "20:18"
func formatBarClock(t time.Time, w, leftW int) string {
	if t.IsZero() {
		t = time.Now()
	}
	free := w - leftW - 1 // ≥1-space gap
	full := t.Format("Mon Jan 2 2006 · 15:04:05 MST")
	if free >= lipgloss.Width(full) {
		return full
	}
	med := t.Format("15:04:05")
	if free >= lipgloss.Width(med) {
		return med
	}
	return t.Format("15:04")
}
