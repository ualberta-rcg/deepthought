package kit

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func plain(s string) string { return sgr.ReplaceAllString(s, "") }

func TestMatchIsSubsequenceAndRanksContiguous(t *testing.T) {
	if _, _, ok := Match("stg", "Settings"); !ok {
		t.Fatal("subsequence s-t-g should match Settings")
	}
	if _, _, ok := Match("gst", "Settings"); ok {
		t.Fatal("out-of-order letters must not match")
	}
	a, _, _ := Match("set", "Settings")
	b, _, _ := Match("set", "Session tokens")
	if a <= b {
		t.Fatalf("contiguous prefix should outrank scattered hits: %d vs %d", a, b)
	}
	_, pos, _ := Match("nc", "New chat")
	if len(pos) != 2 || pos[0] != 0 || pos[1] != 4 {
		t.Fatalf("positions = %v, want [0 4]", pos)
	}
}

func TestKeyBarFitsAndNeverDangles(t *testing.T) {
	keys := []Key{{"enter", "select"}, {"esc", "back"}, {"/", "filter"}, {"?", "keys"}, {"d", "delete"}}
	full := plain(KeyBar(keys, 200))
	for _, w := range []int{8, 20, 33, 47, 60, lipgloss.Width(full)} {
		got := KeyBar(keys, w)
		if lipgloss.Width(got) > w {
			t.Fatalf("width %d: bar is %d wide: %q", w, lipgloss.Width(got), plain(got))
		}
		p := strings.TrimRight(plain(got), " ")
		if strings.HasSuffix(p, keySep) || strings.HasSuffix(plain(got), " ") {
			t.Fatalf("width %d: dangling separator: %q", w, plain(got))
		}
		if w < lipgloss.Width(full) && !strings.HasSuffix(p, G().Ellipsis) {
			t.Fatalf("width %d: truncated bar must end with an ellipsis: %q", w, p)
		}
	}
	if strings.Contains(full, G().Ellipsis) {
		t.Fatalf("a bar that fits must not be truncated: %q", full)
	}
}

func TestPanelExactSizeAndStatusDrop(t *testing.T) {
	p := Panel{Title: "Session", Status: "12s ago", Body: []string{"model qwen3", "a very long line that must be clipped to the panel width"}, Footnote: "r refresh"}
	for _, w := range []int{20, 32, 44} {
		for _, h := range []int{3, 4, 6, 10} {
			out := strings.Split(p.Render(w, h), "\n")
			if len(out) != h {
				t.Fatalf("%dx%d: %d rows", w, h, len(out))
			}
			for _, ln := range out {
				if lipgloss.Width(ln) != w {
					t.Fatalf("%dx%d: row width %d: %q", w, h, lipgloss.Width(ln), plain(ln))
				}
			}
		}
	}
	narrow := plain(strings.Split(p.Render(16, 5), "\n")[0])
	if strings.Contains(narrow, "ago") {
		t.Fatalf("status must be dropped when it cannot fit: %q", narrow)
	}
	if !strings.Contains(plain(p.Render(44, 6)), "r refresh") {
		t.Fatal("footnote missing when there is room")
	}
}

func TestFitHeightsShrinkOrder(t *testing.T) {
	sizes := []Size{{3, 6}, {4, 8}, {3, 5}}
	if got := FitHeights(sizes, 30); got[0] != 6 || got[1] != 8 || got[2] != 5 {
		t.Fatalf("roomy = %v", got)
	}
	if got := FitHeights(sizes, 8); got[0] != 4 || got[1] != 4 || got[2] != 0 {
		t.Fatalf("tight = %v, want [4 4 0] (lowest priority dropped first)", got)
	}
}

func TestListFilterKeysAndOverflow(t *testing.T) {
	l := List{Items: []Item{
		{Label: "New chat", Key: "F5"}, {Label: "Settings", Key: "F1"}, {Label: "Status", Key: "F12"},
		{Label: "Resume chat", Key: "F6"}, {Label: "Sidebar", Key: "F10"},
	}}
	rows := l.Render(40, 5, true)
	if len(rows) != 5 || !strings.Contains(plain(rows[0]), "F5") {
		t.Fatalf("keys should show at 40 cols: %q", plain(strings.Join(rows, "|")))
	}
	for _, r := range rows {
		if lipgloss.Width(r) != 40 {
			t.Fatalf("row width %d", lipgloss.Width(r))
		}
	}
	if narrow := l.Render(10, 5, true); strings.Contains(plain(narrow[0]), "F5") {
		t.Fatal("key column must hide when it exceeds a quarter of the row")
	}
	l.SetFilter("chat")
	if vis := l.Visible(); len(vis) != 2 {
		t.Fatalf("filter chat = %v", vis)
	}
	l.SetFilter("zzz")
	if got := plain(l.Render(40, 3, true)[0]); !strings.Contains(got, "No match") {
		t.Fatalf("empty filter result = %q", got)
	}
	l.SetFilter("")
	for i := 0; i < 4; i++ {
		l.Move(1)
	}
	out := plain(strings.Join(l.Render(40, 4, true), "\n"))
	if !strings.Contains(out, "Sidebar") || !strings.Contains(out, "more") {
		t.Fatalf("cursor row must stay visible with overflow markers:\n%s", out)
	}
}

func TestDialogFitsSmallTerminal(t *testing.T) {
	d := Dialog{Title: "Credentials to review", Info: "3 providers", Body: make([]string, 30), Help: []Key{{"enter", "apply"}, {"esc", "cancel"}}}
	for i := range d.Body {
		d.Body[i] = "provider row"
	}
	out := strings.Split(d.Render(40, 12), "\n")
	if len(out) > 12 {
		t.Fatalf("dialog is %d rows in a 12-row area", len(out))
	}
	for _, ln := range out {
		if lipgloss.Width(ln) > 40 {
			t.Fatalf("dialog row %d wide", lipgloss.Width(ln))
		}
	}
}

func TestASCIIGlyphsAreASCII(t *testing.T) {
	SetASCII(true)
	defer SetASCII(false)
	g := G()
	for _, s := range []string{g.Check, g.Cross, g.Warn, g.Dot, g.Arrow, g.Bullet, g.Cursor, g.Ellipsis, g.BarFull, g.BarEmpty, g.Up, g.Down, g.TL, g.TR, g.BL, g.BR, g.H, g.V} {
		for _, r := range s {
			if r > 127 {
				t.Fatalf("ASCII glyph %q is not ASCII", s)
			}
		}
	}
	out := plain(Panel{Title: "Host", Body: []string{"ok"}}.Render(20, 4))
	for _, r := range out {
		if r > 127 {
			t.Fatalf("ASCII panel contains %q", r)
		}
	}
	if !DetectASCII([]string{"DEEPTHOUGHT_ASCII=1"}) || !DetectASCII([]string{"TERM=dumb"}) || DetectASCII([]string{"TERM=xterm-256color"}) {
		t.Fatal("DetectASCII env handling")
	}
}
