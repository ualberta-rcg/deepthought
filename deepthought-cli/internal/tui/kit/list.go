package kit

import (
	"fmt"
	"sort"

	"charm.land/lipgloss/v2"
)

// Item is one list row: a label (matched by the filter), a one-line detail,
// and an optional bound key shown right-aligned.
type Item struct {
	Label, Detail, Key string
	// Value is the caller's payload (an action id, a chat id, …).
	Value any
}

// List is a filterable, scrollable selection list. It holds no terminal
// state; screens own one and feed it keys through Move/SetFilter.
type List struct {
	Items  []Item
	Filter string
	// Empty is the phrase shown when there is nothing to list ("None").
	Empty string

	cursor int // index into Visible()
	offset int // first visible row
}

type match struct {
	idx   int
	score int
	pos   []int
}

func (l *List) matches() []match {
	var out []match
	for i, it := range l.Items {
		if score, pos, ok := Match(l.Filter, it.Label); ok {
			out = append(out, match{i, score, pos})
		}
	}
	if l.Filter != "" {
		sort.SliceStable(out, func(a, b int) bool { return out[a].score > out[b].score })
	}
	return out
}

// Visible returns the item indexes that pass the filter, best match first.
func (l *List) Visible() []int {
	ms := l.matches()
	out := make([]int, len(ms))
	for i, m := range ms {
		out[i] = m.idx
	}
	return out
}

// SetFilter replaces the filter and puts the cursor on the best match.
func (l *List) SetFilter(f string) {
	l.Filter = f
	l.cursor, l.offset = 0, 0
}

// Move shifts the cursor by d rows, wrapping at both ends.
func (l *List) Move(d int) {
	n := len(l.Visible())
	if n == 0 {
		l.cursor = 0
		return
	}
	l.cursor = ((l.cursor+d)%n + n) % n
}

// Cursor is the cursor position within Visible().
func (l *List) Cursor() int { return l.cursor }

// Selected returns the item under the cursor.
func (l *List) Selected() (Item, bool) {
	vis := l.Visible()
	if len(vis) == 0 {
		return Item{}, false
	}
	return l.Items[vis[min(l.cursor, len(vis)-1)]], true
}

// Render draws exactly h rows of width w. Matched letters are highlighted;
// the key column is hidden for every row when the widest key would take more
// than a quarter of the row; overflow shows "↑ N more" / "↓ N more" rows.
func (l *List) Render(w, h int, focused bool) []string {
	if h <= 0 || w <= 0 {
		return nil
	}
	g := G()
	ms := l.matches()
	if len(ms) == 0 {
		phrase := l.Empty
		if phrase == "" {
			phrase = "None"
		}
		if l.Filter != "" {
			phrase = fmt.Sprintf("No match for %q", l.Filter)
		}
		return Block([]string{muted().Render(phrase)}, w, h)
	}
	l.cursor = min(l.cursor, len(ms)-1)

	keyW := 0
	for _, m := range ms {
		keyW = max(keyW, lipgloss.Width(l.Items[m.idx].Key))
	}
	if keyW*4 > w {
		keyW = 0
	}

	rows := h
	if len(ms) > h {
		rows = max(1, h-2) // reserve the two overflow indicators
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+rows {
		l.offset = l.cursor - rows + 1
	}
	l.offset = max(0, min(l.offset, len(ms)-rows))

	var out []string
	if len(ms) > h {
		out = append(out, muted().Render(fmt.Sprintf("%s %d more", g.Up, l.offset)))
	}
	for i := l.offset; i < min(len(ms), l.offset+rows); i++ {
		out = append(out, l.row(ms[i], i == l.cursor, focused, w, keyW))
	}
	if len(ms) > h {
		out = append(out, muted().Render(fmt.Sprintf("%s %d more", g.Down, len(ms)-l.offset-rows)))
	}
	return Block(out, w, h)
}

func (l *List) row(m match, sel, focused bool, w, keyW int) string {
	it := l.Items[m.idx]
	g := G()
	prefix := "  "
	labelStyle := text()
	if sel {
		prefix = g.Cursor + " "
		if focused {
			labelStyle = accent().Bold(true)
		}
	}
	right := ""
	if keyW > 0 && it.Key != "" {
		right = " " + Fit(muted().Render(it.Key), keyW)
	} else if keyW > 0 {
		right = " " + Fit("", keyW)
	}
	avail := w - lipgloss.Width(prefix) - lipgloss.Width(right)
	label := Highlight(it.Label, m.pos, labelStyle)
	if lipgloss.Width(label) > avail {
		label = Fit(labelStyle.Render(it.Label), avail)
	}
	body := label
	if it.Detail != "" && lipgloss.Width(label)+3 < avail {
		body += muted().Render("  " + it.Detail)
	}
	return prefix + Fit(body, avail) + right
}
