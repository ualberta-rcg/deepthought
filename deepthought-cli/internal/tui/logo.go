package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// The boot mark: two parallel slanted bars sliced into six horizontal spectrum
// bands, 1985 home-computer style. It is nothing but ANSI SGR sequences, so
// it survives SSH; color fidelity depends on the client terminal's profile
// (truecolor -> 256 -> 16 -> none).
//
// Geometry is the user's original v1 sketch — 7-cell bars, 5-cell gap, 2
// cells of lean per row. markFull grows that picture to 20 rows for the boot
// header; markCompact is the original 12, kept for terminals too small for
// the full header.

// markSpec is one size of the mark picture.
type markSpec struct {
	rows  int // picture height
	bar   int // block cells per bar
	gap   int // cells between the two bars
	slope int // cells of horizontal shift per row
}

var (
	markFull    = markSpec{rows: 12, bar: 7, gap: 10, slope: 1}
	markCompact = markSpec{rows: 12, bar: 7, gap: 10, slope: 1}
)

// Six-band spectrum, red at the leading tip, blue trailing. These hexes
// quantize cleanly onto the xterm-256 cube, so the bands stay distinct even
// when an SSH client negotiates down from truecolor.
var markBands = []string{
	"#E53935", // red
	"#FB8C00", // orange
	"#FDD835", // yellow
	"#9CCC65", // yellow-green
	"#43A047", // green
	"#1E88E5", // blue
}

// bandForRow returns the spectrum hex for row r of a mark rows tall, after
// the rainbow has drifted drift rows upward (the splash advances drift every
// few spinner frames). The six bands map proportionally across the mark's
// rows (20 rows for 6 bands ≈ 3.3 rows per band), and row r shows the color
// that sat drift rows below it, so the bands flow toward the top of the
// screen and wrap around as a ring. Floor division/mod keep any drift safe.
func bandForRow(r, drift, rows int) string {
	return markBands[floorMod(floorDiv((r+drift)*len(markBands), rows), len(markBands))]
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func floorMod(a, n int) int { return ((a % n) + n) % n }

// width is the column count of the widest (top) row.
func (m markSpec) width() int {
	return (m.rows-1)*m.slope + m.bar + m.gap + m.bar
}

// row returns the uncolored cells of mark row r: leading indent plus the two
// bars.
func (m markSpec) row(r int) string {
	bar := strings.Repeat("█", m.bar)
	gap := strings.Repeat(" ", m.gap)
	indent := strings.Repeat(" ", (m.rows-1-r)*m.slope)
	return indent + bar + gap + bar
}

// render paints the mark in full color, the spectrum drifted drift rows
// upward (0 = at rest, red at the leading tip).
func (m markSpec) render(drift int) string {
	var b strings.Builder
	for r := 0; r < m.rows; r++ {
		sty := lipgloss.NewStyle().Foreground(lipgloss.Color(bandForRow(r, drift, m.rows)))
		b.WriteString(sty.Render(m.row(r)))
		if r < m.rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderPlain is the colorless ASCII fallback for TERM=dumb / piped output.
func (m markSpec) renderPlain() string {
	bar := strings.Repeat("/", m.bar)
	gap := strings.Repeat(" ", m.gap)

	var b strings.Builder
	for r := 0; r < m.rows; r++ {
		b.WriteString(strings.Repeat(" ", (m.rows-1-r)*m.slope))
		b.WriteString(bar + gap + bar)
		if r < m.rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
