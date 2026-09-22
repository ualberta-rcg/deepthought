package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/unimatrix"
)

// models.go: the F11 Models screen — the model catalog's own home ("we want
// to do more with it"). List / inline edit / add-from-provider / test / role
// assign / delete. The editor kit and field defs are shared with Settings
// (settings_form.go, providerFieldDefs/modelFieldDefs).

type modelsView int

const (
	mvList     modelsView = iota // the catalog
	mvEdit                       // one model's field list (inline editor within)
	mvRole                       // role-assignment mini-picker
	mvProvPick                   // pick a provider to list models from
	mvListed                     // discovered model ids
)

// ModelsModel is the F11 Models screen.
type ModelsModel struct {
	store ConfigStore
	dirty config.File

	view   modelsView
	cursor int

	edit    *fieldEdit
	editIdx int

	// mvEdit context
	entityRef string // model id at open time

	// async: test + discovery
	testing       string
	testResult    string
	listed        []string
	catalog       []babel.CatalogEntry
	catalogCancel context.CancelFunc
	listedProv    string
	listedSel     int

	saved  string
	vp     viewport.Model
	width  int
	height int
}

func NewModelsModel(store ConfigStore) ModelsModel {
	m := ModelsModel{store: store, view: mvList, vp: viewport.New()}
	if store != nil {
		m.dirty = store.Snapshot()
	}
	return m
}

// CapturingKeys: an inline text editor is consuming raw keystrokes.
func (m ModelsModel) CapturingKeys() bool { return m.edit != nil && m.edit.kind == fText }

func (m ModelsModel) Init() tea.Cmd { return nil }

// --- async messages -------------------------------------------------------------

type modelTestResultMsg struct {
	modelID string
	ok      bool
	latency time.Duration
	text    string
	err     error
}
type modelsListedMsg struct {
	provider string
	ids      []string
	entries  []babel.CatalogEntry
	notice   string
	err      error
}

// --- update -----------------------------------------------------------------------

func (m ModelsModel) Update(msg tea.Msg) (ModelsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case modelTestResultMsg:
		m.testing = ""
		if msg.err != nil {
			m.testResult = "✗ " + msg.err.Error()
		} else {
			m.testResult = fmt.Sprintf("✓ %s replied in %s", msg.modelID, msg.latency.Round(time.Millisecond))
		}
		m.saved = m.testResult
		return m, nil
	case modelsListedMsg:
		if msg.err != nil {
			m.saved = "✗ " + msg.err.Error()
			m.view = mvList
			return m, nil
		}
		if len(msg.ids) == 0 {
			m.saved = msg.provider + " listed no models"
			m.view = mvList
			return m, nil
		}
		m.listed, m.listedProv, m.listedSel, m.view, m.cursor = msg.ids, msg.provider, 0, mvListed, 0
		m.catalog = msg.entries
		m.saved = msg.notice
		return m, nil
	}
	// Blink ticks reach an open text editor.
	if m.edit != nil && m.edit.kind == fText {
		if _, ok := msg.(tea.KeyPressMsg); !ok {
			var cmd tea.Cmd
			m.edit.input, cmd = m.edit.input.Update(msg)
			return m, cmd
		}
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.edit != nil {
		return m.updateField(key)
	}
	switch key.String() {
	case "esc", "q":
		return m.escBack()
	case "pgup", "pgdown", "home", "end":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case "up", "k":
		if rows := m.rowCount(); rows > 0 {
			m.cursor = (m.cursor - 1 + rows) % rows
			m.saved = ""
		}
	case "down", "j":
		if rows := m.rowCount(); rows > 0 {
			m.cursor = (m.cursor + 1) % rows
			m.saved = ""
		}
	case "enter":
		return m.activate()
	default:
		if m.view == mvList {
			switch key.String() {
			case "t":
				return m.testModel()
			case "L":
				return m.pickProvider()
			case "r":
				m.view, m.cursor = mvRole, 0
				return m, nil
			case "d":
				return m.deleteModel()
			}
		}
	}
	return m, nil
}

func (m ModelsModel) escBack() (ModelsModel, tea.Cmd) {
	switch m.view {
	case mvList:
		return m, Back()
	case mvEdit, mvRole, mvProvPick, mvListed:
		m.view, m.cursor = mvList, 0
		m.clampCursor()
	}
	return m, nil
}

// activate interprets enter per view.
func (m ModelsModel) activate() (ModelsModel, tea.Cmd) {
	switch m.view {
	case mvList:
		if m.cursor >= len(m.dirty.Models) {
			return m.pickProvider() // "+ Add from provider"
		}
		m.entityRef = m.dirty.Models[m.cursor].ID
		m.view, m.cursor = mvEdit, 0
		return m, nil
	case mvEdit:
		return m.openField()
	case mvRole:
		return m.toggleRole()
	case mvProvPick:
		if m.cursor >= len(m.dirty.Providers) {
			return m, nil
		}
		return m.listModels(m.dirty.Providers[m.cursor].Name)
	case mvListed:
		if m.cursor >= len(m.listed) {
			return m, nil
		}
		return m.addFromList()
	}
	return m, nil
}

// --- edit -------------------------------------------------------------------------+

func (m ModelsModel) openField() (ModelsModel, tea.Cmd) {
	defs := modelFieldDefs(m.entityRef, m.providerNames())
	if m.cursor >= len(defs) {
		return m, nil
	}
	d := defs[m.cursor]
	var edit *fieldEdit
	switch d.kind {
	case fText:
		edit = newTextEdit(d.label, d.get(&m.dirty), d.password)
	case fEnum:
		edit = newEnumEdit(d.label, d.options, d.get(&m.dirty))
	case fMulti:
		edit = newMultiEdit(d.label, d.options, d.getMulti(&m.dirty))
	}
	edit.setWidth(m.width)
	m.edit, m.editIdx = edit, m.cursor
	return m, m.focusCmd()
}

func (m ModelsModel) focusCmd() tea.Cmd {
	if m.edit != nil && m.edit.kind == fText {
		return m.edit.input.Focus()
	}
	return nil
}

func (m ModelsModel) updateField(key tea.KeyPressMsg) (ModelsModel, tea.Cmd) {
	switch key.String() {
	case "enter":
		return m.commitField()
	case "esc":
		m.edit = nil
		return m, nil
	}
	m.edit.update(key)
	return m, nil
}

// commitField re-bases on a fresh snapshot, applies, validates, saves.
func (m ModelsModel) commitField() (ModelsModel, tea.Cmd) {
	defs := modelFieldDefs(m.entityRef, m.providerNames())
	if m.editIdx < 0 || m.editIdx >= len(defs) {
		m.edit = nil
		return m, nil
	}
	d := defs[m.editIdx]
	fresh := m.dirty
	if m.store != nil {
		fresh = m.store.Snapshot()
	}
	if err := d.set(&fresh, m.edit); err != nil {
		m.saved = "✗ " + err.Error()
		return m, nil
	}
	if _, err := config.Validate(fresh); err != nil {
		m.saved = "✗ " + err.Error()
		return m, nil
	}
	if m.store != nil {
		if err := m.store.Save(fresh); err != nil {
			m.saved = "✗ " + err.Error()
			return m, nil
		}
		m.dirty = m.store.Snapshot()
	} else {
		m.dirty = fresh
	}
	if d.label == "id" {
		m.entityRef = strings.TrimSpace(m.edit.value())
	}
	m.edit, m.saved = nil, "saved"
	return m, nil
}

// --- role assignment ---------------------------------------------------------------

// toggleRole assigns/unassigns the selected model to the highlighted role.
func (m ModelsModel) toggleRole() (ModelsModel, tea.Cmd) {
	roles := unimatrix.Roles()
	if m.cursor >= len(roles) || m.entityRef == "" {
		return m, nil
	}
	role, id := roles[m.cursor], m.entityRef
	assign := m.dirty.Roles[role] != id
	return m.freshSave(func(f *config.File) {
		if f.Roles == nil && assign {
			f.Roles = map[string]string{}
		}
		if assign {
			f.Roles[role] = id
		} else if f.Roles[role] == id {
			delete(f.Roles, role)
		}
	})
}

// --- delete -------------------------------------------------------------------------+

func (m ModelsModel) deleteModel() (ModelsModel, tea.Cmd) {
	if m.cursor >= len(m.dirty.Models) {
		return m, nil
	}
	id := m.dirty.Models[m.cursor].ID
	for role, rid := range m.dirty.Roles {
		if rid == id {
			m.saved = fmt.Sprintf("can't delete %q — role %q uses it", id, role)
			return m, nil
		}
	}
	return m.freshSave(func(f *config.File) {
		for i, mo := range f.Models {
			if mo.ID == id {
				f.Models = append(f.Models[:i], f.Models[i+1:]...)
				return
			}
		}
	})
}

// --- test + discovery -----------------------------------------------------------------+

// testModel fires a tiny chat to verify the selected model works.
func (m ModelsModel) testModel() (ModelsModel, tea.Cmd) {
	if m.cursor >= len(m.dirty.Models) || m.testing != "" {
		return m, nil
	}
	id := m.dirty.Models[m.cursor].ID
	wireID := m.dirty.Models[m.cursor].RequestID()
	m.testing = id
	m.saved = "testing " + id + "…"
	store := m.store
	return m, tea.Cmd(func() tea.Msg {
		client, err := store.ClientFor(id)
		if err != nil {
			return modelTestResultMsg{modelID: id, err: err}
		}
		start := time.Now()
		rep, err := client.Chat(context.Background(), babel.ChatRequest{
			Model: wireID, Messages: []babel.Message{{Role: "user", Content: "Reply with exactly: OK"}}, MaxTokens: 16,
		})
		if err != nil {
			return modelTestResultMsg{modelID: id, latency: time.Since(start), err: err}
		}
		return modelTestResultMsg{modelID: id, ok: true, latency: time.Since(start), text: rep.Text}
	})
}

// pickProvider shows the provider chooser for discovery (or lists directly
// when only one provider exists).
func (m ModelsModel) pickProvider() (ModelsModel, tea.Cmd) {
	switch len(m.dirty.Providers) {
	case 0:
		m.saved = "add a provider first (Settings › Providers)"
		return m, nil
	case 1:
		return m.listModels(m.dirty.Providers[0].Name)
	}
	m.view, m.cursor = mvProvPick, 0
	return m, nil
}

// listModels fetches /models from the named provider.
func (m ModelsModel) listModels(name string) (ModelsModel, tea.Cmd) {
	return m.discoverModels(name, true)
}

func (m ModelsModel) Discover(name string) (ModelsModel, tea.Cmd) {
	return m.discoverModels(name, false)
}

func (m ModelsModel) discoverModels(name string, refresh bool) (ModelsModel, tea.Cmd) {
	if m.catalogCancel != nil {
		m.catalogCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	m.catalogCancel = cancel
	m.saved = "listing " + name + "…"
	store := m.store
	return m, tea.Cmd(func() tea.Msg {
		defer cancel()
		var entries []babel.CatalogEntry
		var notice string
		var err error
		if cached, ok := store.(interface {
			DiscoverCatalog(context.Context, string, bool) ([]babel.CatalogEntry, string, error)
		}); ok {
			entries, notice, err = cached.DiscoverCatalog(ctx, name, refresh)
		} else {
			client, err := store.ProviderClient(name)
			if err != nil {
				return modelsListedMsg{provider: name, err: err}
			}
			entries, err = client.ListCatalog(ctx)
		}
		var ids []string
		for _, e := range entries {
			ids = append(ids, e.ID)
		}
		return modelsListedMsg{provider: name, ids: ids, entries: entries, notice: notice, err: err}
	})
}

// addFromList adds the discovered id as a chat-capable model.
func (m ModelsModel) addFromList() (ModelsModel, tea.Cmd) {
	wireID := m.listed[m.cursor]
	id := m.listedProv + "::" + wireID
	entry := babel.CatalogEntry{ID: wireID}
	for _, e := range m.catalog {
		if e.ID == wireID {
			entry = e
		}
	}
	m.view, m.cursor = mvList, 0
	return m.freshSave(func(f *config.File) {
		for _, mo := range f.Models {
			if mo.ID == id {
				return // already present
			}
		}
		caps := []unimatrix.Capability{}
		if entry.Type == "chat" {
			caps = append(caps, unimatrix.CapChat)
		}
		for _, pair := range []struct {
			key string
			cap unimatrix.Capability
		}{{"tools", unimatrix.CapTools}, {"reasoning", unimatrix.CapReasoning}, {"vision", unimatrix.CapVision}} {
			if entry.Capabilities[pair.key] {
				caps = append(caps, pair.cap)
			}
		}
		model := unimatrix.Model{ID: id, WireID: wireID, Label: wireID, Provider: m.listedProv, Context: entry.Context, Capabilities: caps, ReasoningStyle: "none"}
		f.Models = append(f.Models, model)
		if f.Roles == nil {
			f.Roles = map[string]string{}
		}
		if model.Can(unimatrix.CapChat) {
			f.Roles[unimatrix.RoleChat] = id
		}
		if model.Agentic() {
			f.Roles[unimatrix.RoleAgentic] = id
		}
	})
}

// freshSave mutates a FRESH snapshot, validates, saves, re-snapshots.
func (m ModelsModel) freshSave(mut func(f *config.File)) (ModelsModel, tea.Cmd) {
	if m.store == nil {
		mut(&m.dirty)
		m.saved = "saved"
		return m, nil
	}
	fresh := m.store.Snapshot()
	mut(&fresh)
	if _, err := config.Validate(fresh); err != nil {
		m.saved = "✗ " + err.Error()
		return m, nil
	}
	if err := m.store.Save(fresh); err != nil {
		m.saved = "✗ " + err.Error()
		return m, nil
	}
	m.dirty = m.store.Snapshot()
	m.saved = "saved"
	return m, nil
}

// --- rows + rendering --------------------------------------------------------------------

func (m ModelsModel) rowCount() int {
	switch m.view {
	case mvList:
		return len(m.dirty.Models) + 1 // + Add from provider
	case mvEdit:
		return len(modelFieldDefs(m.entityRef, m.providerNames()))
	case mvRole:
		return len(unimatrix.Roles())
	case mvProvPick:
		return len(m.dirty.Providers)
	case mvListed:
		return len(m.listed)
	}
	return 0
}

func (m ModelsModel) providerNames() []string {
	out := make([]string, len(m.dirty.Providers))
	for i, p := range m.dirty.Providers {
		out[i] = p.Name
	}
	return out
}

func (m ModelsModel) clampCursor() {
	n := m.rowCount()
	if n == 0 {
		m.cursor = 0
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// Resize stores geometry and sizes the viewport + editor.
func (m ModelsModel) Resize(w, h int) ModelsModel {
	m.width, m.height = w, h
	m.vp.SetWidth(w - 4)
	bh := h - 2 - 1 - 1
	if bh < 1 {
		bh = 1
	}
	m.vp.SetHeight(bh)
	if m.edit != nil {
		m.edit.setWidth(w - 4)
	}
	return m
}

// rows builds the rendered list for the current view.
func (m ModelsModel) rows() []string {
	switch m.view {
	case mvList:
		rs := make([]string, 0, len(m.dirty.Models)+1)
		if len(m.dirty.Models) == 0 {
			rs = append(rs, "  "+emptyRow("models"))
		}
		for i, mo := range m.dirty.Models {
			rs = append(rs, m.mark(i, fmt.Sprintf("%s %s %s %s",
				truncatePad(mo.ID, 20), truncatePad(mo.Provider, 12), truncatePad(strings.Join(mo.Caps(), "+"), 14), m.roleBadges(mo.ID))))
		}
		rs = append(rs, m.mark(len(m.dirty.Models), "+ Add from provider"))
		return rs
	case mvEdit:
		defs := modelFieldDefs(m.entityRef, m.providerNames())
		rs := make([]string, 0, len(defs))
		for i, d := range defs {
			if m.edit != nil && i == m.editIdx {
				rs = append(rs, styleEditActive.Render("▶ ")+m.edit.view(m.width))
				continue
			}
			val := d.get(&m.dirty)
			if d.password {
				val = mask(val)
			}
			if d.kind == fMulti {
				val = strings.Join(d.getMulti(&m.dirty), "+")
			}
			rs = append(rs, m.mark(i, settingRow(d.label, val)))
		}
		return rs
	case mvRole:
		rs := make([]string, 0, len(unimatrix.Roles()))
		for i, role := range unimatrix.Roles() {
			assigned := "—"
			if m.dirty.Roles[role] == m.entityRef {
				assigned = "this model"
			} else if id := m.dirty.Roles[role]; id != "" {
				assigned = id
			}
			rs = append(rs, m.mark(i, settingRow(role, assigned)))
		}
		return rs
	case mvProvPick:
		rs := make([]string, 0, len(m.dirty.Providers))
		for i, p := range m.dirty.Providers {
			rs = append(rs, m.mark(i, fmt.Sprintf("%s %s %s", truncatePad(p.Name, 16), truncatePad(p.Wire, 7), strings.Join(p.Tags, ", "))))
		}
		return rs
	case mvListed:
		rs := make([]string, 0, len(m.listed))
		for i, id := range m.listed {
			badge := " "
			if m.modelExists(id) {
				badge = "✓"
			}
			rs = append(rs, m.mark(i, badge+" "+id))
		}
		return rs
	}
	return nil
}

// roleBadges renders the dim [chat][agentic] badges for a model id.
func (m ModelsModel) roleBadges(id string) string {
	var badges []string
	for _, role := range unimatrix.Roles() {
		if m.dirty.Roles[role] == id {
			badges = append(badges, "["+role+"]")
		}
	}
	if len(badges) == 0 {
		return ""
	}
	return styleSettingsFoot.Render(strings.Join(badges, ""))
}

func (m ModelsModel) modelExists(id string) bool {
	for _, mo := range m.dirty.Models {
		if mo.ID == id {
			return true
		}
	}
	return false
}

func (m ModelsModel) mark(i int, text string) string {
	if i == m.cursor {
		return styleMenuSel.Render("▶ " + text)
	}
	return styleMenuUnsel.Render("  " + text)
}

// View renders the catalog.
func (m ModelsModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	rows := m.rows()
	m.vp.SetContent(strings.Join(rows, "\n"))
	m.keepCursorVisible()

	title := screenTitle("Models")
	if m.saved != "" {
		gap := m.width - 6 - lipgloss.Width(title) - lipgloss.Width(styleToast.Render(m.saved))
		if gap < 1 {
			gap = 1
		}
		title += strings.Repeat(" ", gap) + styleToast.Render(m.saved)
	}
	return AppScreenScroll(m.width, m.height, title, m.vp.View(), m.vp.Height(), KeyBar(m.keybar()))
}

func (m *ModelsModel) keepCursorVisible() {
	h := m.vp.Height()
	if h <= 0 {
		return
	}
	y := m.vp.YOffset()
	if m.cursor < y {
		m.vp.SetYOffset(m.cursor)
	} else if m.cursor >= y+h {
		m.vp.SetYOffset(m.cursor - h + 1)
	}
}

func (m ModelsModel) keybar() []KeyHint {
	if m.edit != nil {
		if m.edit.kind == fMulti {
			return []KeyHint{{"↑↓", "move"}, {"space", "toggle"}, {"enter", "save"}, {"esc", "cancel"}}
		}
		return []KeyHint{{"enter", "save"}, {"esc", "cancel"}}
	}
	switch m.view {
	case mvList:
		return []KeyHint{
			{"↑↓", "move"}, {"enter", "edit"}, {"L", "add"}, {"t", "test"},
			{"r", "roles"}, {"d", "delete"}, {"esc", "back"},
		}
	case mvEdit:
		return []KeyHint{{"↑↓", "move"}, {"enter", "edit"}, {"esc", "back"}}
	case mvRole:
		return []KeyHint{{"↑↓", "move"}, {"enter", "toggle"}, {"esc", "back"}}
	case mvProvPick:
		return []KeyHint{{"↑↓", "move"}, {"enter", "list"}, {"esc", "back"}}
	case mvListed:
		return []KeyHint{{"↑↓", "move"}, {"enter", "add"}, {"esc", "back"}}
	}
	return nil
}
