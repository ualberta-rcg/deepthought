// Package tui holds DeepThought's Bubble Tea v2 screen models and the shared
// styles/logo they render with. The root model (package app) is the only type
// that satisfies tea.Model; the structs here are plain sub-models that return
// their own concrete type from Update, so the root never type-asserts.
package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Palette. Named by ROLE, not hue, so a retheme touches only this file.
var (
	colPrimary   = lipgloss.Color("#7DD3FC") // cyan  — logo anchor, borders, selected row, "you"
	colSecondary = lipgloss.Color("#C084FC") // violet — help text, status component
	colDim       = lipgloss.Color("240")     // muted grey (ANSI 256) — tagline, hints, footers
	colSuccess   = lipgloss.Color("#86EFAC") // green
	colWarning   = lipgloss.Color("#FCD34D") // amber — toasts, Queen review
	colDanger    = lipgloss.Color("#FCA5A5") // red
	colOnAccent  = lipgloss.Color("#000000") // black on cyan (selected row text)
	colUserBg    = lipgloss.Color("236")     // ANSI256 near-black — tinted band behind user echo
	// Bar chrome: dark slate that stays visible under ANSI 256 (forced at
	// startup for PuTTY). ANSI 236 collapses to black in 16-color mode; 238
	// (#444444) is still "almost black" but readable as a band.
	colBarBg    = lipgloss.Color("238")
	colBarClock = lipgloss.Color("252")
	colBarHint  = lipgloss.Color("246")
	colBarText  = lipgloss.Color("250")
)

// Splash styles.
var (
	styleTagline = lipgloss.NewStyle().Foreground(colDim).Italic(true)
	styleVersion = lipgloss.NewStyle().Foreground(colDim)
	styleVerb    = lipgloss.NewStyle().Foreground(colPrimary)
	styleName    = lipgloss.NewStyle().Foreground(colPrimary).Bold(true) // "// DeepThought" beside the logo
)

// Menu styles.
var (
	styleMenuBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colPrimary).
			Padding(1, 2)
	styleMenuSel   = lipgloss.NewStyle().Background(colPrimary).Foreground(colOnAccent).Bold(true)
	styleMenuUnsel = lipgloss.NewStyle().Foreground(colDim)
	styleMenuFoot  = lipgloss.NewStyle().Foreground(colDim).Italic(true)
	styleToast     = lipgloss.NewStyle().Foreground(colWarning)
)

// Settings styles.
var (
	styleSettingsTitle = lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	styleSettingsKey   = lipgloss.NewStyle().Foreground(colSecondary)
	styleSettingsVal   = lipgloss.NewStyle().Foreground(colPrimary)
	styleSettingsFoot  = lipgloss.NewStyle().Foreground(colDim).Italic(true)
	styleKeyBarKey     = lipgloss.NewStyle().Foreground(colOnAccent).Background(colPrimary).Bold(true)
	styleEditActive    = lipgloss.NewStyle().Foreground(colSuccess).Bold(true) // the ▶ marker on the field being edited
	styleEditValue     = lipgloss.NewStyle().Foreground(colPrimary)            // the typed value in an active input
	styleTab           = lipgloss.NewStyle().Foreground(colDim).Padding(0, 2)
	styleTabActive     = lipgloss.NewStyle().Foreground(colOnAccent).Background(colPrimary).Bold(true).Padding(0, 2)
	styleFormBox       = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colSecondary).
				Padding(1, 2)
)

// Chat styles.
var (
	styleInputBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colDim).
			Padding(0, 1)

	// Transcript echoes (reference style: tinted block for user input, dim for the rest).
	stylePromptPrefix = lipgloss.NewStyle().Foreground(colDim)              // dim "❯"
	styleUserEcho     = lipgloss.NewStyle().Background(colUserBg)           // full-width tinted row (.Width set at render)
	styleSlashEcho    = lipgloss.NewStyle().Foreground(colDim)              // dim "❯ /cmd"
	styleSystem       = lipgloss.NewStyle().Foreground(colDim)              // dim help/system lines (no prefix)
	styleAssistant    = lipgloss.NewStyle().Foreground(colPrimary)          // model reply — "● <text>"
	styleError        = lipgloss.NewStyle().Foreground(colDanger)           // failed call / gateway error
	styleThinking     = lipgloss.NewStyle().Foreground(colDim).Italic(true) // reasoning trace — "∴ <trace>"
	styleTool         = lipgloss.NewStyle().Foreground(colSecondary)        // tool-call chrome — "▸ bash: …"
	styleToolResult   = lipgloss.NewStyle().Foreground(colSuccess)          // tool-result chrome — "↳ …"
	styleToolAsk      = lipgloss.NewStyle().Foreground(colWarning)          // permission prompt — "⚠ allow? [y/n]"

	// Status fields (used by the top/bottom chrome bars). Background is set
	// on the band itself via styleTopBar so every cell paints dark grey.
	styleTopBar      = lipgloss.NewStyle().Background(colBarBg)
	styleStatusApp   = lipgloss.NewStyle().Foreground(colPrimary).Background(colBarBg).Bold(true)
	styleStatusComp  = lipgloss.NewStyle().Foreground(colSecondary).Background(colBarBg)
	styleStatusModel = lipgloss.NewStyle().Foreground(colSuccess).Background(colBarBg) // active model
	styleStatusMode  = lipgloss.NewStyle().Foreground(colWarning).Background(colBarBg)
	styleStatusAddr  = lipgloss.NewStyle().Foreground(colDim).Background(colBarBg)
	styleStatusSep   = lipgloss.NewStyle().Foreground(colDim).Background(colBarBg)
	styleStatusHint  = lipgloss.NewStyle().Foreground(colBarHint).Background(colBarBg)
	styleClock       = lipgloss.NewStyle().Foreground(colBarClock).Background(colBarBg)
	styleBarPad      = lipgloss.NewStyle().Background(colBarBg) // blank cells that keep the band solid
	styleBarText     = lipgloss.NewStyle().Foreground(colBarText).Background(colBarBg)
	styleBarKey      = lipgloss.NewStyle().Foreground(colBarClock).Background(colBarBg) // F-key token in the legend row
)

// placeCenter centers a rendered block in a w×h area (used by splash & menu).
func placeCenter(w, h int, block string) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, block)
}

// bar renders a frac·width progress bar with filled (▓) and empty (░) cells,
// tinted by status color. Used by the Status page (CPU usage, etc.).
func bar(frac float64, width int) string {
	if width < 1 {
		width = 1
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	s := styleSettingsVal.Render(strings.Repeat("▓", filled)) + styleSettingsFoot.Render(strings.Repeat("░", width-filled))
	return s
}

// spinnerGlyph renders spinner frame i as a single braille glyph painted in a
// cycling logo-band color (red → orange → yellow → green → blue, then repeat).
// The bubbles spinner.Model drives only the tick timing; we color the glyph
// ourselves because a spinner.Model applies one Style to every frame, and we want
// each frame in a different color. Callers track the frame index locally (the
// spinner's own frame field is unexported) and pass it here.
func spinnerGlyph(i int) string {
	frames := DefaultSpinner.Frames
	if len(frames) == 0 {
		return "·"
	}
	glyph := frames[i%len(frames)]
	c := lipgloss.Color(markBands[i%len(markBands)])
	return lipgloss.NewStyle().Foreground(c).Render(glyph)
}
