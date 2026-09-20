package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// Section is one block on the Status page: a header, an aligned body, an optional
// plain-language note, and an optional "run X" source. Every section on the page
// builds one of these (see statusSections), so the whole page speaks a single
// visual language — the same header, note, and source treatment for the plain
// sections (Session, Usage, …) and the Slurm ones (Cluster, Your dirs, …).
// Note is bare text — Render adds the standard indent; Source is a bare command
// (hintNote adds the "  → " prefix).
type Section struct {
	Title  string   // required — rendered as "» Title"
	Extra  string   // optional dim summary, right of the header ("2 running · 1 pending")
	Rows   []string // pre-rendered body rows (each already styled)
	Note   string   // optional — one dimNote line ("what this means")
	Source string   // optional — one hintNote line ("→ cmd")
}

// Render emits the block: header + body + note (if any) + source (if any) + a
// trailing blank row. The page trims trailing blanks and inserts its own single
// separator between sections, so the trailing blank here only normalizes spacing.
func (s Section) Render() []string {
	out := []string{sectionHead(s.Title, s.Extra)}
	out = append(out, s.Rows...)
	if s.Note != "" {
		out = append(out, dimNote("  "+s.Note))
	}
	if s.Source != "" {
		out = append(out, hintNote(s.Source))
	}
	out = append(out, "")
	return out
}

// statusLabelW is the fixed width the plain sections' label column is padded to,
// so the value after each "label  value" row lines up (e.g. model/effort/health).
const statusLabelW = 9

// healthChip renders the Session "is the model reachable" state: green
// "✓ reachable" or red "✗ <reason>".
func healthChip(ok bool, reason string) string {
	if ok {
		return lipgloss.NewStyle().Foreground(colSuccess).Render("✓ reachable")
	}
	return lipgloss.NewStyle().Foreground(colDanger).Render("✗ " + orDefault(reason, "unavailable"))
}

// stateChip renders a provider's cached reachability: ok → green, degraded →
// amber (a warning, not an error), idle → dim. One-line colored token.
func stateChip(state string) string {
	switch state {
	case "ok":
		return lipgloss.NewStyle().Foreground(colSuccess).Render("ok")
	case "degraded":
		return lipgloss.NewStyle().Foreground(colWarning).Render("degraded")
	default:
		return lipgloss.NewStyle().Foreground(colDim).Render("idle")
	}
}

// detChip renders a single detection ✓/✗ (cvmfs/module/slurm) on the Login node
// row: green when present, red when not.
func detChip(b bool) string {
	if b {
		return lipgloss.NewStyle().Foreground(colSuccess).Render("✓")
	}
	return lipgloss.NewStyle().Foreground(colDanger).Render("✗")
}

// contextMeter renders the Usage section's context-usage meter: a bar + "NN%
// used/window" when the active model declares a context window, else just the
// used count (clean degrade when the window is unknown). The utilization ramp
// (teal→orange→red) is reused: a near-full context is a warning, like a near-full
// queue.
func contextMeter(used, window int) string {
	if window <= 0 {
		return formatTokens(used)
	}
	frac := frac01(float64(used), float64(window))
	return healthBar(frac, 10) + fmt.Sprintf("  %d%%  %s/%s",
		fracPct(frac), formatTokens(used), formatTokens(window))
}
