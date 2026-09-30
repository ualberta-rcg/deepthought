package kit

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// Match reports whether every rune of pattern appears in s in order
// (case-insensitive subsequence), the positions matched (rune indexes into s),
// and a score: higher for contiguous runs, word starts and an early first hit.
// An empty pattern matches everything with score 0.
func Match(pattern, s string) (score int, pos []int, ok bool) {
	p := []rune(strings.ToLower(strings.TrimSpace(pattern)))
	if len(p) == 0 {
		return 0, nil, true
	}
	r := []rune(s)
	lower := []rune(strings.ToLower(s))
	if len(lower) != len(r) {
		r = lower // case mapping changed the rune count; match on the folded form
	}
	pi := 0
	prev := -2
	for i := 0; i < len(lower) && pi < len(p); i++ {
		if lower[i] != p[pi] {
			continue
		}
		score++
		if i == prev+1 {
			score += 3
		}
		if i == 0 || !unicode.IsLetter(r[i-1]) && !unicode.IsDigit(r[i-1]) {
			score += 2
		}
		pos = append(pos, i)
		prev = i
		pi++
	}
	if pi < len(p) {
		return 0, nil, false
	}
	score -= pos[0] / 4
	return score, pos, true
}

// Highlight renders s with the runes at pos in the match colour and the rest
// in base.
func Highlight(s string, pos []int, base lipgloss.Style) string {
	if len(pos) == 0 {
		return base.Render(s)
	}
	hl := base.Foreground(theme.Match).Bold(true)
	set := make(map[int]bool, len(pos))
	for _, p := range pos {
		set[p] = true
	}
	var b strings.Builder
	var run []rune
	inHL := false
	flush := func() {
		if len(run) == 0 {
			return
		}
		if inHL {
			b.WriteString(hl.Render(string(run)))
		} else {
			b.WriteString(base.Render(string(run)))
		}
		run = run[:0]
	}
	for i, c := range []rune(s) {
		if set[i] != inHL {
			flush()
			inHL = set[i]
		}
		run = append(run, c)
	}
	flush()
	return b.String()
}
