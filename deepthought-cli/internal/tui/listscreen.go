package tui

import (
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/tui/kit"
)

// listNav holds the shared list keys for full-screen lists: ↑↓ / k j /
// ctrl+p ctrl+n move, `/` starts a filter, and while filtering typed text
// goes to the filter (enter keeps it, esc clears it).
type listNav struct{ filtering bool }

// key applies k to l and reports whether it was consumed.
func (n *listNav) key(l *kit.List, k tea.KeyPressMsg) bool {
	if n.filtering {
		switch k.String() {
		case "esc":
			n.filtering = false
			l.SetFilter("")
		case "enter":
			n.filtering = false
		case "backspace":
			if f := []rune(l.Filter); len(f) > 0 {
				l.SetFilter(string(f[:len(f)-1]))
			}
		case "up", "ctrl+p":
			l.Move(-1)
		case "down", "ctrl+n":
			l.Move(1)
		default:
			if k.Text != "" {
				l.SetFilter(l.Filter + k.Text)
			}
		}
		return true
	}
	switch k.String() {
	case "up", "k", "ctrl+p":
		l.Move(-1)
	case "down", "j", "ctrl+n":
		l.Move(1)
	case kit.Mnemonics.Filter:
		n.filtering = true
	case "esc":
		if l.Filter == "" {
			return false
		}
		l.SetFilter("")
	default:
		return false
	}
	return true
}

// keys returns the key bar for the list: the filter's own keys while typing,
// else extra followed by move / filter / back.
func (n listNav) keys(extra ...kit.Key) []kit.Key {
	if n.filtering {
		return []kit.Key{{Key: "type", Help: "filter"}, {Key: "enter", Help: "keep"}, {Key: "esc", Help: "clear"}}
	}
	keys := []kit.Key{{Key: "↑↓", Help: "move"}}
	keys = append(keys, extra...)
	return append(keys, kit.Key{Key: kit.Mnemonics.Filter, Help: "filter"}, kit.Key{Key: "esc", Help: "back"})
}

// renderListScreen draws the shared list + detail layout inside the app
// frame: an optional notice, the filter line when active, the list, and the
// selection's detail card below it. The detail card gets up to half the rows;
// the list keeps at least three.
func renderListScreen(w, h int, title, notice string, l kit.List, nav listNav, detail *kit.Panel, keys []kit.Key) string {
	if w == 0 || h == 0 {
		return ""
	}
	inner := max(1, w-4)
	head := []string{""}
	if notice != "" {
		for _, ln := range strings.Split(lipgloss.NewStyle().Width(inner).Render(notice), "\n") {
			head = append(head, styleToast.Render(ln))
		}
	}
	if nav.filtering || l.Filter != "" {
		cursor := ""
		if nav.filtering {
			cursor = kit.G().BarFull
		}
		head = append(head, styleSettingsKey.Render(kit.Mnemonics.Filter+" ")+l.Filter+cursor)
	}
	avail := max(1, h-4-len(head))
	listH, detailH := avail, 0
	if detail != nil {
		natural := len(detail.Body) + 2
		if detail.Footnote != "" {
			natural++
		}
		fit := kit.FitHeights([]kit.Size{
			{Min: min(3, len(l.Items)+1), Preferred: max(1, len(l.Items))},
			{Min: min(natural, 4), Preferred: min(natural, avail/2)},
		}, avail)
		detailH = fit[1]
		listH = max(1, avail-detailH)
	}
	rows := append(head, l.Render(inner, listH, !nav.filtering)...)
	if detailH > 0 {
		rows = append(rows, strings.Split(detail.Render(inner, detailH), "\n")...)
	}
	return AppScreen(w, h, screenTitle(title), strings.Join(rows, "\n"), kit.KeyBar(keys, inner))
}
