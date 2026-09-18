package tui

import (
	"regexp"
	"strings"
	"testing"
)

func TestBandForRow(t *testing.T) {
	tests := []struct {
		name       string
		row, drift int
		want       string
	}{
		{"rest top is red", 0, 0, markBands[0]},
		{"rest bottom is blue", markFull.rows - 1, 0, markBands[len(markBands)-1]},
		{"sub-band drift keeps color", 0, 1, markBands[0]},                // 1*6/12 = 0
		{"drift up one band", 0, 2, markBands[1]},                         // 2*6/12 = 1
		{"drift wraps at the bottom", markFull.rows - 1, 2, markBands[0]}, // 13*6/12 = 6 → wraps
		{"full height is a full cycle", 0, markFull.rows, markBands[0]},
		{"negative drift is safe", 0, -4, markBands[4]}, // floorDiv(-24,20) = -2 → mod 6 = 4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bandForRow(tt.row, tt.drift, markFull.rows); got != tt.want {
				t.Errorf("bandForRow(%d, %d, %d) = %s, want %s",
					tt.row, tt.drift, markFull.rows, got, tt.want)
			}
		})
	}
}

// The compact mark is the original 12-row sketch: same picture, fewer rows.
func TestCompactMark(t *testing.T) {
	if got := bandForRow(0, 0, markCompact.rows); got != markBands[0] {
		t.Errorf("compact top at rest = %s, want %s", got, markBands[0])
	}
	if got := bandForRow(markCompact.rows-1, 0, markCompact.rows); got != markBands[len(markBands)-1] {
		t.Errorf("compact bottom at rest = %s, want %s", got, markBands[len(markBands)-1])
	}
	if got := markCompact.width(); got != 35 { // 11*1 + 7+10+7
		t.Errorf("compact width = %d, want 35", got)
	}
}

// cellIndex returns the index of target in s counted in runes (terminal
// cells), because the mark's "█" glyphs are 3 bytes each and strings.Index
// would report byte offsets.
func cellIndex(s string, target rune) int {
	n := 0
	for _, r := range s {
		if r == target {
			return n
		}
		n++
	}
	return -1
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]|\x1b[=>]")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func testWord(n int) []string {
	word := make([]string, n)
	for i := range word {
		word[i] = "W"
	}
	return word
}

// The header is two pictures side by side: the 16-row wordmark sits 2 rows
// inside the 20-row mark at top and bottom (header height stays 20), and
// every wordmark row starts at the same column — the mark's widest row plus
// the gap.
func TestSplashHeaderSideBySide(t *testing.T) {
	need := markFull.width() + headerGapCols + 1

	header := splashHeader(need, markFull.rows, 0, testWord(8))
	lines := strings.Split(stripANSI(header), "\n")
	if len(lines) != markFull.rows {
		t.Fatalf("side-by-side header: %d lines, want %d", len(lines), markFull.rows)
	}

	wantCol := markFull.width() + headerGapCols
	for r, ln := range lines {
		idx := cellIndex(ln, 'W')
		if r < 2 || r > markFull.rows-3 { // 2-row inset (12-row mark, 8-row word)
			if idx != -1 {
				t.Errorf("row %d: wordmark present in the inset zone (col %d)", r, idx)
			}
			continue
		}
		if idx != wantCol {
			t.Errorf("row %d: wordmark starts at col %d, want %d (mark width + gap)", r, idx, wantCol)
		}
	}
}

// splashHeader degrades through the ladder: side by side → stacked → compact.
func TestSplashHeaderLadder(t *testing.T) {
	word := testWord(8)
	need := markFull.width() + headerGapCols + 1

	stacked := splashHeader(need-1, markFull.rows+1+len(word), 0, word)
	if got := len(strings.Split(stacked, "\n")); got != markFull.rows+1+len(word) {
		t.Errorf("stacked layout: %d lines, want %d", got, markFull.rows+1+len(word))
	}

	compact := splashHeader(need-1, markFull.rows+1+len(word)-1, 0, word)
	if got := len(strings.Split(compact, "\n")); got != markCompact.rows {
		t.Errorf("compact layout: %d lines, want %d", got, markCompact.rows)
	}

	sideBySide := splashHeader(need, markFull.rows, 0, word)
	if got := len(strings.Split(sideBySide, "\n")); got != markFull.rows {
		t.Errorf("side-by-side layout: %d lines, want %d", got, markFull.rows)
	}
}

// The wordmark is the colossal art for "DeepThought" — 11 rows once blank
// edge rows are trimmed (the tall T and h reach past the baseline the
// shorter old name sat on). Pinning it keeps a font change from silently
// reshaping the splash; the layout itself (splashHeader) is height-generic.
func TestWordmarkHeight(t *testing.T) {
	if word := wordmark(); len(word) != 11 {
		t.Fatalf("wordmark: %d rows, want 11", len(word))
	}
}

func TestDoubleRows(t *testing.T) {
	in := []string{"a", "bb", "ccc"}
	got := doubleRows(in)
	if len(got) != 6 {
		t.Fatalf("doubleRows: %d rows, want 6", len(got))
	}
	for i, ln := range in {
		if got[2*i] != ln || got[2*i+1] != ln {
			t.Errorf("row %d: got %q/%q, want %q twice", i, got[2*i], got[2*i+1], ln)
		}
	}
}
