package app

import (
	"os"

	"charm.land/bubbletea/v2"
	"charm.land/wish/v2/bubbletea"
	"github.com/charmbracelet/ssh"

	"deepthought-cli/internal/tui"
)

// SSHHandler returns the wish/bubbletea handler for the given listen address. d
// carries the config-built status cluster + shared inference client; only Addr is
// filled per connection. The returned function runs ONCE PER SSH CONNECTION and
// must build a fresh RootModel (NewRootModel builds a fresh ChatModel) — concurrent
// sessions never share state. (Sharing one model across sessions is the classic
// Wish foot-gun.) The client pointer is the one shared thing, and it's safe for
// concurrent use.
func SSHHandler(addr string, d Deps) bubbletea.Handler {
	return func(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
		d.Status.Addr = addr
		d.Settings.Addr = addr
		// Per-session Queen: clone so task/session grants never cross SSH users.
		d.Gate = d.Gate.Clone()
		m := NewRootModel(d)

		// Seed size from the PTY handshake BEFORE the first frame, so the
		// splash doesn't paint at 0×0. The wish/bubbletea middleware also
		// emits tea.WindowSizeMsg on later resizes.
		if pty, _, ok := sess.Pty(); ok {
			w, h := pty.Window.Width, pty.Window.Height
			m.width, m.height = w, h
			m.splash = m.splash.Resize(w, h)
			legend := d.Live != nil && d.Live.TopBarLegend()
			m.chat = m.chat.Resize(w, h-tui.ChatChromeHeight(legend)) // mirror Update's subtraction
			m.settings = m.settings.Resize(w, h)
		}
		// Force ANSI 256 so PuTTY's default TERM=xterm doesn't crush the
		// dark-grey chrome band (and rainbow) down to black.
		environ := sess.Environ()
		if len(environ) == 0 {
			environ = os.Environ()
		}
		return m, ProgramColorOpts(environ)
	}
}
