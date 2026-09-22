package app

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"charm.land/bubbletea/v2"
	"deepthought-cli/internal/tui"
)

func (m *RootModel) showServerSync() {
	notice := "Disconnected — local settings remain saved."
	items := []tui.WorkspaceItem{{Label: "Connect / reconnect", Kind: "connect-server"}, {Label: "Configure URL, user and password", Kind: "settings-tab", ID: "server"}}
	if m.connecting {
		notice = "Connecting…"
	}
	if m.syncWorker != nil {
		st := m.syncWorker.Status()
		notice = st.State
		if m.syncBusy {
			notice = "Synchronizing…"
		}
		if !st.LastSuccess.IsZero() {
			notice += " · last success " + st.LastSuccess.Format(time.RFC3339)
		}
		items = append(items, tui.WorkspaceItem{Label: "Sync now", Detail: st.Detail, Kind: "sync-now"}, tui.WorkspaceItem{Label: "Disconnect", Kind: "disconnect-server"})
		for i, c := range st.Conflicts {
			l, _ := json.Marshal(c.Local)
			r, _ := json.Marshal(c.Remote)
			lv, rv := string(l), string(r)
			if !c.LocalPresent {
				lv = "(deleted)"
			}
			if !c.RemotePresent {
				rv = "(deleted)"
			}
			items = append(items, tui.WorkspaceItem{Label: c.Path + " — keep local", Detail: lv, Kind: "resolve-local", ID: strconv.Itoa(i)}, tui.WorkspaceItem{Label: c.Path + " — use server", Detail: rv, Kind: "resolve-remote", ID: strconv.Itoa(i)})
		}
	}
	if m.connectionNotice != "" {
		notice += " · " + m.connectionNotice
	}
	if s := m.deps.Live; s != nil && s.MirrorNotice() != "" {
		items = append(items, tui.WorkspaceItem{Label: "Config file needs attention", Detail: s.MirrorNotice(), Kind: "settings-tab", ID: "files"})
	}
	cursor := 0
	if m.workspace.Title == "Server synchronization" {
		cursor = m.workspace.Cursor
	}
	m.showWorkspace("Server synchronization", notice, items)
	m.workspace.Cursor = min(cursor, max(0, len(items)-1))
}
func (m RootModel) syncAction(a tui.WorkspaceAction) (tea.Model, tea.Cmd) {
	switch a.Kind {
	case "server-sync":
		m.showServerSync()
	case "connect-server":
		if m.connecting {
			return m, nil
		}
		m.connecting = true
		m.connectionNotice = ""
		m.showServerSync()
		return m, serverLoginCmd(m.deps.Live)
	case "sync-now":
		if m.syncWorker == nil {
			return m.syncAction(tui.WorkspaceAction{Kind: "connect-server"})
		}
		if m.syncBusy {
			return m, nil
		}
		m.syncBusy = true
		m.showServerSync()
		return m, settingsSyncCmd(m.syncWorker)
	case "disconnect-server":
		if m.syncWorker != nil {
			m.syncWorker.Stop()
		}
		m.connecting = false
		m.server = nil
		m.syncWorker = nil
		m.syncBusy = false
		m.connectionNotice = "Disconnected"
		if s := m.localStore(); s != nil {
			_ = s.WriteRecord("server-autoconnect", s.ProfileKey(), false)
		}
		m.showServerSync()
	case "resolve-local", "resolve-remote":
		if m.syncWorker == nil || m.syncBusy {
			return m, nil
		}
		i, err := strconv.Atoi(a.ID)
		st := m.syncWorker.Status()
		if err != nil || i < 0 || i >= len(st.Conflicts) {
			return m, nil
		}
		side := "local"
		if a.Kind == "resolve-remote" {
			side = "remote"
		}
		if err = m.syncWorker.Resolve(st.Conflicts[i].Path, side); err != nil {
			m.connectionNotice = err.Error()
			m.showServerSync()
			return m, nil
		}
		m.syncBusy = true
		m.showServerSync()
		return m, settingsSyncCmd(m.syncWorker)
	case "retry-mirror":
		if s := m.localStore(); s != nil {
			s.RepairMirror()
			m.chat = m.chat.Notice(fmt.Sprintf("Local configuration: %s", func() string {
				if s.MirrorNotice() != "" {
					return s.MirrorNotice()
				}
				return "database and file saved"
			}()))
		}
	}
	return m, nil
}
