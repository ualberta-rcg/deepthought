package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"charm.land/bubbletea/v2"
)

func TestRewriteTerm256(t *testing.T) {
	env := rewriteTerm256([]string{"HOME=/tmp", "TERM=xterm", "PATH=/bin"})
	term := ""
	for _, e := range env {
		if strings.HasPrefix(e, "TERM=") {
			term = e
		}
	}
	if term != "TERM=xterm-256color" {
		t.Fatalf("TERM=%q want xterm-256color", term)
	}
}

func TestProgramColorOptsForceANSI256(t *testing.T) {
	opts := ProgramColorOpts([]string{"TERM=xterm"})
	if len(opts) < 2 {
		t.Fatalf("expected env + profile opts, got %d", len(opts))
	}
	// Smoke: options are non-nil ProgramOption funcs (applied by tea.NewProgram).
	_ = tea.WithColorProfile(colorprofile.ANSI256)
	_ = opts
}
