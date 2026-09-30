package app

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"

	"charm.land/bubbletea/v2"
	"charm.land/wish/v2/bubbletea"
	"github.com/charmbracelet/ssh"
)

// SSHHandler returns the wish/bubbletea handler for the given listen address. d
// carries the config-built status cluster + shared inference client; only Addr is
// filled per connection. The returned function runs ONCE PER SSH CONNECTION and
// must build a fresh RootModel (NewRootModel builds a fresh ChatModel) — concurrent
// sessions never share state. (Sharing one model across sessions is the classic
// Wish foot-gun.) The client pointer is the one shared thing, and it's safe for
// concurrent use.
func SSHHandler(addr string, d Deps) bubbletea.Handler {
	return func(sess ssh.Session) (model tea.Model, opts []tea.ProgramOption) {
		// A panic while building one session must not take down the server
		// and every other connected user with it.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ssh session %s: panic: %v\n%s", sess.User(), r, debug.Stack())
				fmt.Fprintln(sess, "DeepThought could not start this session (the error was logged). Please reconnect.")
				model, opts = nil, nil
			}
		}()
		d := d // never mutate the closure shared by concurrent connections
		d.Context = sess.Context()
		d.Registry = d.Registry.Fork()
		go func() { <-sess.Context().Done(); d.Registry.Close() }()
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
			m.chatResize() // the same sidebar-aware width WindowSizeMsg uses
			m.settings = m.settings.Resize(w, h)
		}
		// Force ANSI 256 so PuTTY's default TERM=xterm doesn't crush the
		// dark-grey chrome band down to black.
		environ := sess.Environ()
		if len(environ) == 0 {
			environ = os.Environ()
		}
		return m, ProgramColorOpts(environ)
	}
}
