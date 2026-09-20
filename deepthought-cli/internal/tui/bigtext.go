package tui

import (
	"strings"

	"github.com/common-nighthawk/go-figure"
)

const (
	// bigTextFont is the splash wordmark's figlet font. "colossal" renders
	// "DON'T PANIC" as the big boot statement. Pulled in via go-figure, which
	// embeds its fonts (bindata) so the binary stays self-contained. TODO:
	// vendor just the fonts we use to drop the unused ~148 if binary size ever
	// matters.
	bigTextFont = "colossal"
	// smallTextFont is the narrow-terminal fallback for the wordmark.
	smallTextFont = "small"
)

// bigText renders s as ASCII-art lines in the splash's big font, trimmed for
// layout. Returns nil if the font can't render the text (the caller falls
// back). Color is the caller's job.
func bigText(s string) []string {
	return bigTextIn(bigTextFont, s)
}

// bigTextIn renders s in the named figlet font, trimmed for layout: trailing
// spaces stripped per row and fully-blank edge rows dropped.
func bigTextIn(font, s string) []string {
	if s == "" {
		return nil
	}
	out := figure.NewFigure(s, font, true).String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}
