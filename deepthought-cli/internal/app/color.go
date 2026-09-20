package app

import (
	"os"
	"strings"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// ProgramColorOpts forces ANSI 256-color output. PuTTY (and many SSH clients)
// advertise TERM=xterm, which Bubble Tea treats as 16-color — near-black greys
// then collapse to black and the chrome band disappears. 256-color keeps the
// dark-slate bar and solid brand wordmark readable without needing client TERM tweaks.
func ProgramColorOpts(environ []string) []tea.ProgramOption {
	if environ == nil {
		environ = os.Environ()
	}
	env := rewriteTerm256(environ)
	return []tea.ProgramOption{
		tea.WithEnvironment(env),
		tea.WithColorProfile(colorprofile.ANSI256),
	}
}

// rewriteTerm256 sets TERM to a 256-color variant when the client sent a
// bare xterm/putty/ansi/dumb value (or nothing). Leaves xterm-256color /
// truecolor TERMs alone aside from ensuring the slice is a copy.
func rewriteTerm256(environ []string) []string {
	out := make([]string, 0, len(environ)+1)
	term := ""
	for _, e := range environ {
		if strings.HasPrefix(e, "TERM=") {
			term = strings.TrimPrefix(e, "TERM=")
			continue // rewrite below
		}
		out = append(out, e)
	}
	switch {
	case strings.Contains(term, "256color"), strings.Contains(term, "truecolor"), term == "alacritty", term == "kitty", term == "wezterm":
		out = append(out, "TERM="+term)
	case term == "" || term == "xterm" || term == "putty" || term == "ansi" || term == "vt100" || term == "screen" || term == "tmux":
		out = append(out, "TERM=xterm-256color")
	default:
		// Unknown but possibly capable — still prefer 256 so chrome stays visible.
		if !strings.Contains(term, "color") {
			out = append(out, "TERM=xterm-256color")
		} else {
			out = append(out, "TERM="+term)
		}
	}
	return out
}
