package app

import (
	"strconv"
	"strings"

	"charm.land/bubbletea/v2"

	commandpkg "deepthought-cli/internal/commands"
	"deepthought-cli/internal/keybindings"
	"deepthought-cli/internal/tui"
)

const (
	paletteRecentKind = "palette-recent"
	paletteRecentID   = "recent"
	paletteRecentMax  = 12
	paletteChatsMax   = 10
)

// slashNeedsArgs lists slash commands that are prefilled, not run, from the palette.
var slashNeedsArgs = map[string]bool{"pin": true, "unpin": true, "export": true}

// paletteEntries is the action registry as the palette sees it: every
// bindable action (with its current key), screen, settings section, recent
// chat and slash command. Each entry dispatches through workspaceAction.
func (m RootModel) paletteEntries() []tui.PaletteEntry {
	discover := map[keybindings.Action]bool{
		keybindings.NewChat: true, keybindings.Resume: true, keybindings.Model: true,
		keybindings.Settings: true, keybindings.Diagnostics: true, keybindings.Help: true,
	}
	var out []tui.PaletteEntry
	for _, info := range keybindings.Actions() {
		key := ""
		if m.bindings != nil {
			key = strings.ToUpper(m.bindings.KeyFor(info.Action))
		}
		out = append(out, tui.PaletteEntry{Category: tui.PaletteActions, Label: info.Label, Detail: info.Detail, Key: key,
			Kind: "action", ID: string(info.Action), Discover: discover[info.Action]})
	}
	out = append(out,
		tui.PaletteEntry{Category: tui.PaletteActions, Label: "Discover AI providers", Detail: "Review credentials and endpoints before connecting", Kind: "discover"},
		tui.PaletteEntry{Category: tui.PaletteActions, Label: "Refresh scientific endpoints", Detail: "Connect to configured tool servers", Kind: "tools"},
		tui.PaletteEntry{Category: tui.PaletteScreens, Label: "Chat", Detail: "Back to the conversation", Kind: "home"},
		tui.PaletteEntry{Category: tui.PaletteScreens, Label: "Jobs", Detail: "Tracked submissions and retry advice", Kind: "screen", ID: strconv.Itoa(int(tui.ScreenJobs))},
		tui.PaletteEntry{Category: tui.PaletteScreens, Label: "Plans", Detail: "Saved scientific plans", Kind: "screen", ID: strconv.Itoa(int(tui.ScreenPlans))},
		tui.PaletteEntry{Category: tui.PaletteScreens, Label: "Hosts and services", Kind: "hosts"},
		tui.PaletteEntry{Category: tui.PaletteScreens, Label: "Server connection and synchronization", Kind: "server-sync", Discover: true},
		tui.PaletteEntry{Category: tui.PaletteScreens, Label: "Install on another machine", Kind: "install"},
	)
	for _, s := range tui.SettingsSections() {
		out = append(out, tui.PaletteEntry{Category: tui.PaletteSettings, Label: "Settings › " + s.Label, Kind: "settings-tab", ID: s.Key})
	}
	if m.deps.ChatSource != nil {
		if sums, err := m.deps.ChatSource().ListCollectives(); err == nil {
			for i, c := range sums {
				if i == paletteChatsMax {
					break
				}
				title := c.Title
				if title == "" {
					title = "Untitled chat"
				}
				out = append(out, tui.PaletteEntry{Category: tui.PaletteChats, Label: title, Detail: c.UpdatedAt.Format("Jan 2 15:04"), Kind: "resume", ID: c.ID})
			}
		}
	}
	for _, c := range commandpkg.Builtins().All() {
		out = append(out, tui.PaletteEntry{Category: tui.PaletteSlash, Label: "/" + c.Name, Detail: c.Description, Kind: "slash", ID: c.Name})
	}
	return out
}

func (m RootModel) paletteRecents() []string {
	var refs []string
	if s := m.localStore(); s != nil {
		_ = s.ReadRecord(paletteRecentKind, paletteRecentID, &refs)
	}
	return refs
}

// rememberPalette moves ref to the front of the persisted recents list.
func (m RootModel) rememberPalette(ref string) {
	s := m.localStore()
	if s == nil {
		return
	}
	refs := []string{ref}
	for _, r := range m.paletteRecents() {
		if r != ref && len(refs) < paletteRecentMax {
			refs = append(refs, r)
		}
	}
	_ = s.WriteRecord(paletteRecentKind, paletteRecentID, refs)
}

func (m *RootModel) openPalette() {
	m.pushOverlay(tui.NewPalette(m.paletteEntries(), m.paletteRecents()))
}

// paletteChosen records the pick and runs it like any menu item.
func (m RootModel) paletteChosen(e tui.PaletteEntry) (tea.Model, tea.Cmd) {
	m.rememberPalette(e.Ref())
	switch e.Kind {
	case "resume":
		return m.update(tui.ResumeChatMsg{CollectID: e.ID})
	case "slash":
		m.screen, m.screenStack = tui.ScreenChat, nil
		if slashNeedsArgs[e.ID] {
			m.chat = m.chat.SetInput("/" + e.ID + " ")
			return m, nil
		}
		var cmd tea.Cmd
		m.chat, cmd = m.chat.RunCommand("/" + e.ID)
		return m, cmd
	}
	return m.workspaceAction(tui.WorkspaceAction{Kind: e.Kind, ID: e.ID})
}
