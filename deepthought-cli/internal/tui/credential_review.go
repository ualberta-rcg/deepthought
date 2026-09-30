package tui

import (
	"fmt"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/tui/kit"
)

// CredentialReviewRow is one provider in the credential sync review.
type CredentialReviewRow struct {
	Provider string
	Detail   string
	Options  []string
	Choice   int
}

// CredentialReviewDoneMsg carries the picks (option index per row), or
// Cancelled when the user skipped the whole review.
type CredentialReviewDoneMsg struct {
	Picks     []int
	Cancelled bool
}

// CredentialReview is the overlay shown on first login per machine (and for
// conflicts afterwards): ↑↓ pick a provider, ←→/space change its action,
// enter applies, esc skips everything this time.
type CredentialReview struct {
	rows          []CredentialReviewRow
	vault         bool
	cursor        int
	width, height int
	done          bool
}

func NewCredentialReview(rows []CredentialReviewRow, vault bool) CredentialReview {
	return CredentialReview{rows: rows, vault: vault}
}

func (c CredentialReview) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || len(c.rows) == 0 {
		return c, nil
	}
	cycle := func(d int) {
		r := &c.rows[c.cursor]
		if n := len(r.Options); n > 0 {
			r.Choice = ((r.Choice+d)%n + n) % n
		}
	}
	switch key.String() {
	case "up", "k", "ctrl+p":
		c.cursor = (c.cursor - 1 + len(c.rows)) % len(c.rows)
	case "down", "j", "ctrl+n":
		c.cursor = (c.cursor + 1) % len(c.rows)
	case "right", "l", "space", "tab":
		cycle(1)
	case "left", "h", "shift+tab":
		cycle(-1)
	case "enter":
		c.done = true
		picks := make([]int, len(c.rows))
		for i, r := range c.rows {
			picks[i] = r.Choice
		}
		return c, func() tea.Msg { return CredentialReviewDoneMsg{Picks: picks} }
	case "esc":
		c.done = true
		return c, func() tea.Msg { return CredentialReviewDoneMsg{Cancelled: true} }
	}
	return c, nil
}

func (c CredentialReview) Resize(w, h int) Overlay {
	c.width, c.height = w, h
	return c
}

func (c CredentialReview) Done() bool { return c.done }

func (c CredentialReview) View() string {
	g := kit.G()
	w := min(76, max(30, c.width-4))
	inner := w - 4
	body := []string{styleSystem.Render("Provider keys differ between this machine and the server."), ""}
	for i, r := range c.rows {
		prefix := "  "
		name := r.Provider
		if i == c.cursor {
			prefix = g.Cursor + " "
			name = styleSettingsTitle.Render(name)
		}
		choice := ""
		if r.Choice < len(r.Options) {
			choice = styleEditValue.Render("‹ " + r.Options[r.Choice] + " ›")
		}
		body = append(body, kit.Fit(prefix+name+"  "+choice, inner))
		body = append(body, kit.Fit("    "+styleSystem.Render(r.Detail), inner))
	}
	if !c.vault {
		body = append(body, "", styleSystem.Render("The server has no credential vault: literal keys stay on this machine."))
	}
	body = append(body, "", styleToolAsk.Render(g.Warn)+styleSystem.Render(" Shared-password server: every password holder can read synced keys."))
	return kit.Dialog{
		Title: "Sync provider keys",
		Info:  fmt.Sprintf("%d providers", len(c.rows)),
		Body:  body,
		Help:  []kit.Key{{Key: "←→", Help: "change"}, {Key: "↑↓", Help: "move"}, {Key: "enter", Help: "apply"}, {Key: "esc", Help: "skip for now"}},
		Width: w,
	}.Render(c.width, c.height)
}
