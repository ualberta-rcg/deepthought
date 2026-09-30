package app

// Server › Provider keys and Server › Sessions: masked lists of what the
// server holds for this account, with per-row delete / revoke.

import (
	"errors"
	"time"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/credential"
	"deepthought-cli/internal/tui"
)

const sharedPasswordNote = "Shared-password server: every password holder can read these keys."

type serverSessionRow struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
	Expires  time.Time `json:"expires"`
}

func (s *ServerSession) listSessions() (current string, rows []serverSessionRow, err error) {
	var out struct {
		Current  string             `json:"current"`
		Sessions []serverSessionRow `json:"sessions"`
	}
	err = s.do("GET", "/api/v1/user/sessions", nil, &out)
	if unsupported(err) {
		err = errCredentialsUnsupported
	}
	return out.Current, out.Sessions, err
}

func (s *ServerSession) revokeSession(id string) error {
	return s.do("DELETE", "/api/v1/user/sessions/"+id, nil, nil)
}

type serverKeysMsg struct {
	Index credentialIndex
	Err   error
}

type serverSessionsMsg struct {
	Current string
	Rows    []serverSessionRow
	Err     error
}

type serverAccountDoneMsg struct {
	Kind   string // "server-credentials" or "server-sessions" to reopen
	Notice string
	Err    error
}

func serverKeysCmd(sess *ServerSession) tea.Cmd {
	return func() tea.Msg {
		idx, err := sess.credentialIndex()
		return serverKeysMsg{Index: idx, Err: err}
	}
}

func serverSessionsCmd(sess *ServerSession) tea.Cmd {
	return func() tea.Msg {
		cur, rows, err := sess.listSessions()
		return serverSessionsMsg{Current: cur, Rows: rows, Err: err}
	}
}

func (m RootModel) serverAccountAction(a tui.WorkspaceAction) (tea.Model, tea.Cmd) {
	if m.server == nil {
		m.connectionNotice = "Connect to the server first."
		m.showServerSync()
		return m, nil
	}
	sess := m.server
	switch a.Kind {
	case "server-credentials":
		m.showWorkspace("Provider keys on server", "Loading…", nil)
		return m, serverKeysCmd(sess)
	case "server-sessions":
		m.showWorkspace("Sessions on this account", "Loading…", nil)
		return m, serverSessionsCmd(sess)
	case "delete-credential":
		m.showWorkspace("Delete provider key", "Removes the server copy only; this machine keeps its key.", []tui.WorkspaceItem{
			{Label: "Delete " + a.ID[:min(8, len(a.ID))] + "… from the server", Kind: "confirm-delete-credential", ID: a.ID},
			{Label: "Cancel", Kind: "server-credentials"},
		})
	case "confirm-delete-credential":
		return m, func() tea.Msg {
			return serverAccountDoneMsg{Kind: "server-credentials", Notice: "Deleted from the server.", Err: sess.DeleteCredential(a.ID)}
		}
	case "revoke-session":
		return m, func() tea.Msg {
			return serverAccountDoneMsg{Kind: "server-sessions", Notice: "Session revoked.", Err: sess.revokeSession(a.ID)}
		}
	}
	return m, nil
}

func (m RootModel) serverKeys(msg serverKeysMsg) (tea.Model, tea.Cmd) {
	if m.workspace.Title != "Provider keys on server" {
		return m, nil
	}
	if errors.Is(msg.Err, ErrSessionExpired) {
		m.sessionExpired()
		return m, nil
	}
	back := tui.WorkspaceItem{Label: "Back to server synchronization", Kind: "server-sync"}
	if errors.Is(msg.Err, errCredentialsUnsupported) {
		m.showWorkspace("Provider keys on server", "This server does not store provider keys.", []tui.WorkspaceItem{back})
		return m, nil
	}
	if msg.Err != nil {
		m.showWorkspace("Provider keys on server", "⚠ "+credential.Redact(msg.Err.Error()), []tui.WorkspaceItem{{Label: "Retry", Kind: "server-credentials"}, back})
		return m, nil
	}
	var items []tui.WorkspaceItem
	for _, c := range msg.Index.Credentials {
		detail := c.Kind + fingerprintTail(c.Fingerprint) + " · updated " + c.UpdatedAt.Local().Format("2006-01-02 15:04")
		items = append(items, tui.WorkspaceItem{Label: c.Name + " · " + c.BaseURL, Detail: detail, Kind: "delete-credential", ID: c.ID})
	}
	notice := sharedPasswordNote
	if len(items) == 0 {
		notice = "None. " + notice
	} else {
		notice = "Select a key to delete its server copy. " + notice
	}
	if !msg.Index.Vault {
		notice += " No vault: only $VARIABLE references are stored."
	}
	m.showWorkspace("Provider keys on server", notice, append(items, back))
	return m, nil
}

func (m RootModel) serverSessions(msg serverSessionsMsg) (tea.Model, tea.Cmd) {
	if m.workspace.Title != "Sessions on this account" {
		return m, nil
	}
	if errors.Is(msg.Err, ErrSessionExpired) {
		m.sessionExpired()
		return m, nil
	}
	back := tui.WorkspaceItem{Label: "Back to server synchronization", Kind: "server-sync"}
	if msg.Err != nil {
		note := "⚠ " + credential.Redact(msg.Err.Error())
		if errors.Is(msg.Err, errCredentialsUnsupported) {
			note = "This server does not list sessions."
		}
		m.showWorkspace("Sessions on this account", note, []tui.WorkspaceItem{back})
		return m, nil
	}
	var items []tui.WorkspaceItem
	for _, r := range msg.Rows {
		label := "Session " + r.ID[:min(8, len(r.ID))]
		item := tui.WorkspaceItem{Label: label, Detail: "last used " + r.LastSeen.Local().Format("2006-01-02 15:04") + " · expires " + r.Expires.Local().Format("2006-01-02 15:04"), Kind: "revoke-session", ID: r.ID}
		if r.ID == msg.Current {
			item.Label += " (this client)"
			item.Kind = ""
		}
		items = append(items, item)
	}
	m.showWorkspace("Sessions on this account", "Select a session to revoke it.", append(items, back))
	return m, nil
}

func (m RootModel) serverAccountDone(msg serverAccountDoneMsg) (tea.Model, tea.Cmd) {
	if errors.Is(msg.Err, ErrSessionExpired) {
		m.sessionExpired()
		return m, nil
	}
	if msg.Err != nil {
		m.chat = m.chat.Notice("⚠ " + credential.Redact(msg.Err.Error()))
	} else {
		m.chat = m.chat.Notice(msg.Notice)
	}
	return m.serverAccountAction(tui.WorkspaceAction{Kind: msg.Kind})
}
