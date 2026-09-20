package tui

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/keybindings"
	"deepthought-cli/internal/unimatrix"
)

// ConfigStore is the settings editor's seam to the live configuration handle
// (app.Settings satisfies it). The editor reads a snapshot, mutates a local
// copy, and writes back through Save — which persists to disk and hot-swaps
// the running config. tui never imports app, so this interface lives here.
type ConfigStore interface {
	Snapshot() config.File
	Save(config.File) error
	Path() string
	ClientFor(modelID string) (*babel.Client, error)
	ProviderClient(providerName string) (*babel.Client, error)
}

// SettingsInfo is the slim boot-time snapshot the editor shows in its title.
type SettingsInfo struct {
	ConfigPath string
	Mode       string
	Addr       string
}

// settingsTab is one flat section of the editor. The tab row is the only
// top-level navigation (lazygit-style: peer sections as tabs, ≤2 perceptual
// levels — tab list → inline editor — underneath).
type settingsTab struct {
	key   string
	label string
}

var settingsTabs = []settingsTab{
	{"overview", "Overview"},   // config at a glance: health, roles, counts
	{"general", "General"},     // profile + behavior (effort/max-tokens/temperature)
	{"providers", "Providers"}, // backends (advanced fields in the entity editor)
	{"roles", "Roles"},         // role → model assignment
	{"routing", "Routing"},     // declarative routes (capability/cost/prefer)
	{"permissions", "Perms"},
	{"appearance", "Theme"},
	{"system", "System"}, // read-only: host descriptor + storage + keybindings
}

// settingsRoadmap is the dim one-liner under the tab row marking where the
// not-yet-configurable sections live (they were dead "coming soon" tree rows).
const settingsRoadmap = "planned: shell & env · memory · skills · tools · privacy"

// settingsView is the level inside the current tab.
type settingsView int

const (
	viewList   settingsView = iota // the tab's list (entities, fields, rules)
	viewEntity                     // one provider/model's field list
	viewField                      // an inline field editor is open (m.edit != nil)
)

// SettingsModel is the flat, tabbed settings editor. Every field commit
// re-bases on a FRESH store snapshot before saving (so edits never silently
// revert changes made elsewhere — F9 mode, F4 effort, /model…), validates,
// auto-saves, and re-snapshots. Entities being added are staged as in-memory
// drafts until they validate (the store refuses invalid files).
type SettingsModel struct {
	store ConfigStore
	info  SettingsInfo
	env   EnvInfo     // host descriptor → the System tab
	dirty config.File // working copy (render cache + draft staging)

	tab    int          // index into settingsTabs
	view   settingsView // list / entity / field
	cursor int          // row cursor in the current view

	edit    *fieldEdit
	editIdx int // index of the field row being edited

	// entity context (Providers/Models tabs)
	entityKind string // "provider" | "model"
	entityRef  string // stable key: provider name / model id AT OPEN TIME
	adding     bool   // draft entity staged, not yet valid/saved

	// Permissions rule drill-down: "allow" | "ask" | "deny" (empty = top list)
	permBucket string
	permAdding bool

	saved  string // transient toast
	vp     viewport.Model
	width  int
	height int
}

// NewSettingsModel builds the editor from the live config store + boot info.
func NewSettingsModel(store ConfigStore, info SettingsInfo) SettingsModel {
	m := SettingsModel{store: store, info: info, view: viewList, vp: viewport.New()}
	if store != nil {
		m.dirty = store.Snapshot()
	}
	return m
}

// NewSettingsModelAt builds the editor opened directly at a tab's list. When
// add is true the cursor lands on the tab's "+ Add" row (used by the splash to
// drop the user at Providers › + add when no model is configured). tabKey is a
// settingsTabs key ("providers", "models", ...).
func NewSettingsModelAt(store ConfigStore, info SettingsInfo, tabKey string, add bool) SettingsModel {
	m := NewSettingsModel(store, info)
	for i, t := range settingsTabs {
		if t.key == tabKey {
			m.tab = i
			break
		}
	}
	if add {
		m.cursor = m.entityCount()
	}
	return m
}

// SetEnv stamps the host descriptor (drives the System tab).
func (m SettingsModel) SetEnv(e EnvInfo) SettingsModel {
	m.env = e
	return m
}

// CapturingKeys reports whether the editor is consuming raw keystrokes (an
// inline text editor is open), so the root must NOT resolve global key
// bindings — a user-rebound letter would otherwise be swallowed mid-typing.
func (m SettingsModel) CapturingKeys() bool { return m.edit != nil && m.edit.kind == fText }

func (m SettingsModel) Init() tea.Cmd { return nil }

// --- update ------------------------------------------------------------------

func (m SettingsModel) Update(msg tea.Msg) (SettingsModel, tea.Cmd) {
	// Non-key messages (cursor-blink ticks) reach an open text editor so the
	// caret blinks instead of freezing solid.
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
	// Field editing takes all keys until enter/esc.
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
	case "left", "h", "[":
		return m.switchTab(m.tab - 1)
	case "right", "l", "]":
		return m.switchTab(m.tab + 1)
	}
	if n, err := strconv.Atoi(key.String()); err == nil && n >= 1 && n <= len(settingsTabs) {
		return m.gotoTab(n - 1)
	}
	return m.updateList(key)
}

// escBack pops one level: entity → list (finishing or dropping a draft),
// permission bucket → permission list, list → the previous screen.
func (m SettingsModel) escBack() (SettingsModel, tea.Cmd) {
	switch {
	case m.view == viewEntity:
		m.finishAdd()
		m.view = viewList
		m.cursor = 0
		m.clampCursor()
	case m.permBucket != "":
		m.permBucket, m.permAdding, m.cursor = "", false, 0
	default:
		return m, Back()
	}
	return m, nil
}

// switchTab wraps around the tab row; gotoTab jumps (digit keys).
func (m SettingsModel) switchTab(i int) (SettingsModel, tea.Cmd) {
	return m.gotoTab((i + len(settingsTabs)) % len(settingsTabs))
}

func (m SettingsModel) gotoTab(i int) (SettingsModel, tea.Cmd) {
	m.finishAdd()
	m.edit = nil // never carry an open editor across tabs
	m.tab, m.view, m.cursor, m.permBucket, m.permAdding = i, viewList, 0, "", false
	m.saved = ""
	return m, nil
}

// updateList handles ↑↓ movement, enter activation, and the per-tab action
// keys (d delete, t test, L list) across the list views.
func (m SettingsModel) updateList(key tea.KeyPressMsg) (SettingsModel, tea.Cmd) {
	rows := m.rowCount()
	switch key.String() {
	case "up", "k":
		if rows > 0 {
			m.cursor = (m.cursor - 1 + rows) % rows
			m.saved = ""
		}
	case "down", "j":
		if rows > 0 {
			m.cursor = (m.cursor + 1) % rows
			m.saved = ""
		}
	case "enter":
		return m.activate()
	case "d":
		if m.view == viewList && m.tabKeyOf() == "providers" {
			return m.deleteEntity()
		}
	}
	return m, nil
}

func (m SettingsModel) tabKey() string { return settingsTabs[m.tab].label }

// tabKeyOf returns the settingsTabs key of the current tab.
func (m SettingsModel) tabKeyOf() string { return settingsTabs[m.tab].key }

// activate interprets enter on the current row, per tab and view.
func (m SettingsModel) activate() (SettingsModel, tea.Cmd) {
	if m.view == viewEntity {
		return m.openField()
	}
	switch m.tabKeyOf() {
	case "overview", "system":
		return m, nil // read-only tabs
	case "routing":
		return m.enterOrAddRoute()
	case "general", "appearance":
		return m.openScalarField()
	case "providers":
		return m.enterOrAddEntity("provider")
	case "roles":
		return m.cycleRole()
	case "permissions":
		return m.activatePermissions()
	}
	return m, nil // System: read-only
}

// --- permissions -------------------------------------------------------------

func (m SettingsModel) activatePermissions() (SettingsModel, tea.Cmd) {
	if m.permBucket != "" {
		return m.activatePermRules()
	}
	switch m.cursor {
	case 0: // operation mode cycles safe → safe-auto → auto
		order := []string{"safe", "safe-auto", "auto"}
		current := m.permMode()
		at := 0
		for i, v := range order {
			if v == current {
				at = i
			}
		}
		next := order[(at+1)%len(order)]
		return m.persistRebase(func(f *config.File) {
			if f.Permissions == nil {
				f.Permissions = &config.Permissions{}
			}
			f.Permissions.Mode = next
			f.PermissionMode = next
		})
	case 1, 2, 3:
		m.permBucket = []string{"allow", "ask", "deny"}[m.cursor-1]
		m.cursor = 0
		return m, nil
	}
	return m, nil
}

func (m SettingsModel) permMode() string {
	mode := ""
	if m.dirty.Permissions != nil {
		mode = m.dirty.Permissions.Mode
	}
	if mode == "" {
		mode = m.dirty.PermissionMode
	}
	if mode == "" || mode == "review" {
		return "safe"
	}
	if mode == "always-proceed" {
		return "auto"
	}
	return mode
}

// activatePermRules: enter on a rule deletes it; on "+ Add" opens a text
// editor; the keybar documents both.
func (m SettingsModel) activatePermRules() (SettingsModel, tea.Cmd) {
	rules := m.permRules()
	if m.cursor >= len(rules) { // "+ Add rule"
		m.edit = newTextEdit(m.permBucket+" rule", "", false)
		m.edit.setWidth(m.width - 8)
		m.editIdx = -1
		m.permAdding = true
		return m, m.focusCmd()
	}
	bucket, at := m.permBucket, m.cursor
	return m.persistRebase(func(f *config.File) {
		if f.Permissions == nil {
			return
		}
		switch bucket {
		case "allow":
			f.Permissions.Allow = dropAt(f.Permissions.Allow, at)
		case "ask":
			f.Permissions.Ask = dropAt(f.Permissions.Ask, at)
		case "deny":
			f.Permissions.Deny = dropAt(f.Permissions.Deny, at)
		}
	})
}

func dropAt(ss []string, at int) []string {
	if at < 0 || at >= len(ss) {
		return ss
	}
	return append(append([]string{}, ss[:at]...), ss[at+1:]...)
}

func (m SettingsModel) permRules() []string {
	if m.dirty.Permissions == nil {
		return nil
	}
	switch m.permBucket {
	case "allow":
		return m.dirty.Permissions.Allow
	case "ask":
		return m.dirty.Permissions.Ask
	case "deny":
		return m.dirty.Permissions.Deny
	}
	return nil
}

// --- entities (Providers/Models) ----------------------------------------------

// enterOrAddEntity: cursor on an entity row drills into its field list; on
// the "+ Add" row it stages a DRAFT entity (in-memory only — the store refuses
// invalid files, so a half-filled provider could never be saved mid-add).
func (m SettingsModel) enterOrAddEntity(kind string) (SettingsModel, tea.Cmd) {
	if kind != "provider" {
		return m, nil
	}
	n := m.entityCount()
	if m.cursor >= n { // "+ Add"
		if kind == "provider" {
			m.dirty.Providers = append(m.dirty.Providers, config.Provider{Name: "", Wire: "openai"})
			m.entityRef = ""
		}
		m.entityKind, m.adding, m.view, m.cursor = kind, true, viewEntity, 0
		return m, nil
	}
	if kind == "provider" {
		m.entityRef = m.dirty.Providers[m.cursor].Name
	} else {
		m.entityRef = m.dirty.Models[m.cursor].ID
	}
	m.entityKind, m.adding, m.view, m.cursor = kind, false, viewEntity, 0
	return m, nil
}

func (m SettingsModel) entityCount() int {
	return len(m.dirty.Providers)
}

// finishAdd closes a draft: if the working copy now validates, save it; if
// not, drop the draft and say why. This makes "+ Add provider" traversable
// (name → base_url → key → esc) — the old whole-file validation trap is gone.
func (m *SettingsModel) finishAdd() {
	if !m.adding {
		return
	}
	m.adding = false
	if _, err := config.Validate(m.dirty); err != nil {
		m.dropDraft()
		m.saved = "add cancelled — " + err.Error()
		return
	}
	if m.store != nil {
		if err := m.store.Save(m.dirty); err != nil {
			m.saved = "✗ " + err.Error()
			return
		}
		m.dirty = m.store.Snapshot()
	}
	m.saved = "added"
}

// dropDraft removes the staged (still-invalid) entity from the working copy.
func (m *SettingsModel) dropDraft() {
	if m.entityKind == "route" {
		delete(m.dirty.Routes, m.entityRef)
		return
	}
	if m.entityKind == "provider" {
		for i := len(m.dirty.Providers) - 1; i >= 0; i-- {
			if strings.TrimSpace(m.dirty.Providers[i].Name) == m.entityRef {
				m.dirty.Providers = append(m.dirty.Providers[:i], m.dirty.Providers[i+1:]...)
				return
			}
		}
	}
}

// --- field editing -------------------------------------------------------------

// openField opens the selected entity field (viewEntity) for inline editing.
func (m SettingsModel) openField() (SettingsModel, tea.Cmd) {
	defs := m.fieldDefs()
	if m.cursor >= len(defs) {
		return m, nil
	}
	return m.openEdit(defs[m.cursor], m.cursor)
}

// openScalarField opens a General/Appearance field from the tab's flat list.
func (m SettingsModel) openScalarField() (SettingsModel, tea.Cmd) {
	defs := m.fieldDefs()
	if m.cursor < 0 || m.cursor >= len(defs) {
		return m, nil
	}
	return m.openEdit(defs[m.cursor], m.cursor)
}

func (m SettingsModel) openEdit(d fieldDef, idx int) (SettingsModel, tea.Cmd) {
	var edit *fieldEdit
	switch d.kind {
	case fText:
		edit = newTextEdit(d.label, d.get(&m.dirty), d.password)
	case fEnum:
		edit = newEnumEdit(d.label, d.options, d.get(&m.dirty))
	case fMulti:
		edit = newMultiEdit(d.label, d.options, d.getMulti(&m.dirty))
	}
	edit.setWidth(m.width - 8)
	m.edit = edit
	m.editIdx = idx
	return m, m.focusCmd()
}

func (m SettingsModel) focusCmd() tea.Cmd {
	if m.edit != nil && m.edit.kind == fText {
		return m.edit.input.Focus() // arm the cursor blink
	}
	return nil
}

// updateField drives the active editor: enter commits+saves, esc cancels.
func (m SettingsModel) updateField(key tea.KeyPressMsg) (SettingsModel, tea.Cmd) {
	switch key.String() {
	case "enter":
		return m.commitField()
	case "esc":
		m.edit, m.permAdding = nil, false
		return m, nil
	}
	m.edit.update(key)
	return m, nil
}

// commitField writes the edited value back. Drafts (adding) apply to the
// working copy only. Everything else re-bases on a FRESH store snapshot —
// apply the field, validate, save — so concurrent changes made elsewhere
// (mode/effort/model switches) always survive.
func (m SettingsModel) commitField() (SettingsModel, tea.Cmd) {
	// "+ Add rule" in a permissions bucket.
	if m.permAdding && m.permBucket != "" {
		rule := strings.TrimSpace(m.edit.value())
		bucket := m.permBucket
		m.edit, m.permAdding = nil, false
		if rule == "" {
			m.saved = "empty rule ignored"
			return m, nil
		}
		return m.persistRebase(func(f *config.File) {
			if f.Permissions == nil {
				f.Permissions = &config.Permissions{Mode: "safe"}
			}
			switch bucket {
			case "allow":
				f.Permissions.Allow = append(f.Permissions.Allow, rule)
			case "ask":
				f.Permissions.Ask = append(f.Permissions.Ask, rule)
			case "deny":
				f.Permissions.Deny = append(f.Permissions.Deny, rule)
			}
		})
	}

	defs := m.fieldDefs()
	if m.editIdx < 0 || m.editIdx >= len(defs) {
		m.edit = nil
		return m, nil
	}
	d := defs[m.editIdx]

	// Draft entity: apply to the working copy only; the store would reject it.
	if m.adding {
		if err := d.set(&m.dirty, m.edit); err != nil {
			m.saved = "✗ " + err.Error()
			return m, nil
		}
		if d.label == "name" || d.label == "id" {
			m.entityRef = strings.TrimSpace(m.edit.value())
		}
		m.edit = nil
		m.saved = "draft — esc to save (add cancelled if still invalid)"
		return m, nil
	}

	fresh := m.dirty
	if m.store != nil {
		fresh = m.store.Snapshot() // re-base: never clobber concurrent edits
	}
	if err := d.set(&fresh, m.edit); err != nil {
		m.saved = "✗ " + err.Error()
		return m, nil // stay in the editor so the user can fix it
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
	// Keep the entity reference in step with renames (the set above may have
	// changed the name/id this view is keyed by).
	if d.label == "name" || d.label == "id" {
		m.entityRef = strings.TrimSpace(m.edit.value())
	}
	m.edit = nil
	m.saved = "saved"
	return m, nil
}

// persistRebase mutates a FRESH snapshot (never the working copy), validates,
// saves, and re-snapshots — the one safe write path for non-entity edits
// (roles, permissions, deletes).
func (m SettingsModel) persistRebase(mut func(f *config.File)) (SettingsModel, tea.Cmd) {
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

// --- roles ---------------------------------------------------------------------

func (m SettingsModel) cycleRole() (SettingsModel, tea.Cmd) {
	roles := unimatrix.Roles()
	if m.cursor >= len(roles) {
		return m, nil
	}
	role := roles[m.cursor]
	cands := m.roleCandidates(role)
	if len(cands) == 0 {
		return m, nil
	}
	cur := m.dirty.Roles[role]
	at := 0
	for i, c := range cands {
		if c.ID == cur {
			at = i
			break
		}
	}
	next := cands[(at+1)%len(cands)]
	return m.persistRebase(func(f *config.File) {
		if f.Roles == nil {
			f.Roles = map[string]string{}
		}
		f.Roles[role] = next.ID
	})
}

func (m SettingsModel) roleCandidates(role string) []unimatrix.Model {
	if role == unimatrix.RoleAgentic {
		var out []unimatrix.Model
		for _, mo := range m.dirty.Models {
			if mo.Agentic() {
				out = append(out, mo)
			}
		}
		return out
	}
	return m.dirty.Models
}

// --- delete --------------------------------------------------------------------

func (m SettingsModel) deleteEntity() (SettingsModel, tea.Cmd) {
	if m.view != viewList {
		return m, nil
	}
	if m.tabKeyOf() == "providers" {
		if m.cursor >= len(m.dirty.Providers) {
			return m, nil
		}
		name := m.dirty.Providers[m.cursor].Name
		for _, mo := range m.dirty.Models {
			if mo.Provider == name {
				m.saved = fmt.Sprintf("can't delete %q — model %q uses it", name, mo.ID)
				return m, nil
			}
		}
		return m.persistRebase(func(f *config.File) {
			for i, p := range f.Providers {
				if p.Name == name {
					f.Providers = append(f.Providers[:i], f.Providers[i+1:]...)
					return
				}
			}
		})
	}
	return m, nil
}

// --- model test + list models ----------------------------------------------------

// --- row model -------------------------------------------------------------------

// rowCount is the number of navigable rows in the current view.
func (m SettingsModel) rowCount() int {
	if m.view == viewEntity {
		return len(m.fieldDefs())
	}
	switch m.tabKeyOf() {
	case "overview":
		return len(m.overviewRows())
	case "routing":
		return len(m.routeKeys()) + 1 // + Add
	case "system":
		return len(m.systemRows())
	case "general", "appearance":
		return len(m.fieldDefs())
	case "providers":
		return len(m.dirty.Providers) + 1 // + Add
	case "roles":
		return len(unimatrix.Roles())
	case "permissions":
		if m.permBucket != "" {
			return len(m.permRules()) + 1 // + Add rule
		}
		return 4 // mode, allow, ask, deny
	}
	return 0
}

func (m SettingsModel) clampCursor() {
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

// Resize stores geometry and sizes the body viewport + inline editor.
func (m SettingsModel) Resize(w, h int) SettingsModel {
	m.width, m.height = w, h
	inner := w - 4 // inside the frame border + pad
	m.vp.SetWidth(inner)
	bh := h - 2 - 1 - 1 - 2 // border, title, keybar, tab row + roadmap line
	if bh < 1 {
		bh = 1
	}
	m.vp.SetHeight(bh)
	if m.edit != nil {
		m.edit.setWidth(inner)
	}
	return m
}

// --- rendering --------------------------------------------------------------------

// rows builds the rendered list for the current view; the active field editor
// row is swapped in where it sits.
func (m SettingsModel) rows() []string {
	if m.view == viewEntity {
		return m.entityRows()
	}
	switch m.tabKeyOf() {
	case "overview":
		return m.overviewRows()
	case "routing":
		return m.routingRows()
	case "system":
		return m.systemRows()
	case "general", "appearance":
		return m.fieldRows()
	case "providers":
		rs := make([]string, 0, len(m.dirty.Providers)+1)
		if len(m.dirty.Providers) == 0 {
			rs = append(rs, "  "+emptyRow("providers"))
		}
		for i, p := range m.dirty.Providers {
			rs = append(rs, m.mark(i, fmt.Sprintf("%s %s %s", truncatePad(p.Name, 16), truncatePad(p.Wire, 7), strings.Join(p.Tags, ", "))))
		}
		rs = append(rs, m.mark(len(m.dirty.Providers), "+ Add provider"))
		return rs
	case "roles":
		cfg, _ := config.Validate(m.dirty)
		rs := make([]string, 0, len(unimatrix.Roles()))
		for i, role := range unimatrix.Roles() {
			assigned := "—"
			if cfg != nil {
				if mo, err := cfg.RoleModel(role); err == nil {
					assigned = modelLabel(mo)
				}
			}
			rs = append(rs, m.mark(i, settingRow(role, assigned)))
		}
		return rs
	case "permissions":
		return m.permRows()
	}
	return nil
}

// fieldRows paints a flat field list (General/Appearance) with the active
// editor swapped in.
func (m SettingsModel) fieldRows() []string {
	defs := m.fieldDefs()
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
}

// entityRows paints one provider/model's field list with the active editor
// swapped in.
func (m SettingsModel) entityRows() []string {
	defs := m.fieldDefs()
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
}

func (m SettingsModel) permRows() []string {
	if m.permBucket != "" {
		rules := m.permRules()
		rs := make([]string, 0, len(rules)+1)
		for i, r := range rules {
			rs = append(rs, m.mark(i, settingRow(fmt.Sprintf("%d", i+1), r)))
		}
		if m.edit != nil && m.permAdding {
			return append(rs, styleEditActive.Render("▶ ")+m.edit.view(m.width))
		}
		return append(rs, m.mark(len(rules), "+ Add rule"))
	}
	allowN, askN, denyN := 0, 0, 0
	if m.dirty.Permissions != nil {
		allowN = len(m.dirty.Permissions.Allow)
		askN = len(m.dirty.Permissions.Ask)
		denyN = len(m.dirty.Permissions.Deny)
	}
	return []string{
		m.mark(0, settingRow("operation mode", m.permMode())),
		m.mark(1, settingRow("allow rules", fmt.Sprintf("%d", allowN))),
		m.mark(2, settingRow("ask rules", fmt.Sprintf("%d", askN))),
		m.mark(3, settingRow("deny rules", fmt.Sprintf("%d", denyN))),
	}
}

func (m SettingsModel) systemRows() []string {
	e := m.env
	var rows []string
	add := func(k, v string) { rows = append(rows, settingRow(k, v)) }
	add("host", orDefault(e.ShortName, orDefault(e.Host, "?")))
	if e.LongName != "" && e.LongName != e.ShortName {
		add("fqdn", e.LongName)
	}
	add("os", orDefault(e.OSName, "—"))
	add("kernel", orDefault(e.Kernel, "—"))
	add("arch", orDefault(e.Arch, "?"))
	if e.CPUs > 0 {
		spec := fmt.Sprintf("%d", e.CPUs)
		if e.MemGB > 0 {
			spec += fmt.Sprintf(" · %d GB", e.MemGB)
		}
		add("cpus", spec)
	}
	add("home", envOr("HOME", "—")+" · backed up")
	add("scratch", envOr("SCRATCH", "—")+" · 60-day purge")
	add("project", envOr("PROJECT", "—")+" · backed up")
	for i := 1; i <= 12; i++ {
		add(fmt.Sprintf("F%d", i), functionKeyLabel(i))
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = m.mark(i, r)
	}
	return out
}

// mark renders one list row with the cursor highlight.
func (m SettingsModel) mark(i int, text string) string {
	if i == m.cursor {
		return styleMenuSel.Render("▶ " + text)
	}
	return styleMenuUnsel.Render("  " + text)
}

// tabRow renders the tab strip: the active tab as a solid brand chip, the
// rest dim. Clipped to the frame width on narrow terminals (digit keys 1-7
// still reach every tab).
func (m SettingsModel) tabRow() string {
	cells := make([]string, 0, len(settingsTabs))
	for i, t := range settingsTabs {
		if i == m.tab {
			cells = append(cells, lipgloss.NewStyle().
				Foreground(colOnAccent).Background(colPrimary).Bold(true).
				Render(" "+t.label))
		} else {
			cells = append(cells, styleSettingsFoot.Render(" "+t.label))
		}
	}
	return clipLine(strings.Join(cells, "  "), m.width-4)
}

// View renders the editor: title (+ toast) / tab row / roadmap line / the
// current list in a viewport / keybar — one full-width pane, no split.
func (m SettingsModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	rows := m.rows()
	m.vp.SetContent(strings.Join(rows, "\n"))
	m.keepCursorVisible()

	title := screenTitle("Settings")
	if m.saved != "" {
		gap := m.width - 6 - lipgloss.Width(title) - lipgloss.Width(styleToast.Render(m.saved))
		if gap < 1 {
			gap = 1
		}
		title += strings.Repeat(" ", gap) + styleToast.Render(m.saved)
	}
	body := m.tabRow() + "\n" + styleSettingsFoot.Render(settingsRoadmap) + "\n" + m.vp.View()
	return AppScreenScroll(m.width, m.height, title, body, m.vp.Height()+2, KeyBar(m.keybar()))
}

// keepCursorVisible scrolls the viewport so the cursor row is on screen.
func (m *SettingsModel) keepCursorVisible() {
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

// keybar returns the per-state keybinding hints.
func (m SettingsModel) keybar() []KeyHint {
	if m.edit != nil {
		if m.edit.kind == fMulti {
			return []KeyHint{{"↑↓", "move"}, {"space", "toggle"}, {"enter", "save"}, {"esc", "cancel"}}
		}
		return []KeyHint{{"enter", "save"}, {"esc", "cancel"}}
	}
	if m.view == viewEntity {
		return []KeyHint{{"↑↓", "move"}, {"enter", "edit"}, {"esc", "back"}}
	}
	hints := []KeyHint{{"↑↓", "move"}, {"enter", "open"}, {"←/→", "tab"}}
	switch m.tabKeyOf() {
	case "providers":
		hints = append(hints, KeyHint{"d", "delete"})
	}
	return append(hints, KeyHint{"esc", "back"})
}

// --- Overview + Routing (the refill) -------------------------------------------------

// overviewRows: the config at a glance — health, the active model + every
// role assignment, counts, and the file path. Read-only ( Roles edits roles,
// Providers/Models edit entities).
func (m SettingsModel) overviewRows() []string {
	health, healthOK := "valid", true
	if _, err := config.Validate(m.dirty); err != nil {
		health, healthOK = err.Error(), false
	}
	h := styleToolResult.Render("✓ valid")
	if !healthOK {
		h = styleError.Render("✗ " + health)
	}
	rows := []string{
		settingRow("config", h),
		settingRow("file", m.store.Path()),
		settingRow("models", fmt.Sprintf("%d  (F11 manages them)", len(m.dirty.Models))),
		settingRow("providers", fmt.Sprintf("%d", len(m.dirty.Providers))),
		settingRow("routes", fmt.Sprintf("%d  (Routing tab)", len(m.dirty.Routes))),
	}
	if mm, ok := activeModel(m.dirty); ok {
		rows = append(rows, settingRow("running", modelLabel(mm)))
	}
	for _, role := range unimatrix.Roles() {
		assigned := "—"
		if id := m.dirty.Roles[role]; id != "" {
			assigned = id
		}
		rows = append(rows, settingRow(role, assigned))
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = m.mark(i, r)
	}
	return out
}

// routeKeys returns the route names in a stable order.
func (m SettingsModel) routeKeys() []string {
	out := make([]string, 0, len(m.dirty.Routes))
	for k := range m.dirty.Routes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// routingRows: the route list with the cursor marked.
func (m SettingsModel) routingRows() []string {
	rs := []string{}
	for i, k := range m.routeKeys() {
		r := m.dirty.Routes[k]
		spec := string(r.Capability)
		if r.NeedsTools {
			spec += " · tools"
		}
		if r.MaxCost > 0 {
			spec += fmt.Sprintf(" · ≤$%g", r.MaxCost)
		}
		if len(r.Prefer) > 0 {
			spec += " · prefer " + strings.Join(r.Prefer, ",")
		}
		rs = append(rs, m.mark(i, settingRow(k, spec)))
	}
	return append(rs, m.mark(len(m.routeKeys()), "+ Add route"))
}

// enterOrAddRoute: enter on a route opens its field editor; "+ Add" stages a
// draft route (the same draft discipline as providers).
func (m SettingsModel) enterOrAddRoute() (SettingsModel, tea.Cmd) {
	keys := m.routeKeys()
	if m.cursor >= len(keys) { // + Add
		if m.dirty.Routes == nil {
			m.dirty.Routes = map[string]config.Route{}
		}
		m.dirty.Routes["new-route"] = config.Route{Capability: unimatrix.CapGenerate}
		m.entityKind, m.entityRef, m.adding = "route", "new-route", true
		m.view, m.cursor = viewEntity, 0
		return m, nil
	}
	m.entityKind, m.entityRef, m.adding = "route", keys[m.cursor], false
	m.view, m.cursor = viewEntity, 0
	return m, nil
}

// routeFieldDefs: one route's editable fields, keyed by its stable name.
func routeFieldDefs(ref string) []fieldDef {
	find := func(f *config.File) *config.Route {
		if r, ok := f.Routes[ref]; ok {
			return &r
		}
		return nil
	}
	caps := capStrings()
	return []fieldDef{
		{"name", fText, false, nil,
			func(f *config.File) string { return ref }, nil,
			func(f *config.File, e *fieldEdit) error {
				n := strings.TrimSpace(e.value())
				if n == "" {
					return fmt.Errorf("name is required")
				}
				if _, exists := f.Routes[n]; exists && n != ref {
					return fmt.Errorf("route %q already used", n)
				}
				if _, ok := f.Routes[ref]; !ok {
					return fmt.Errorf("route was removed elsewhere")
				}
				f.Routes[n] = f.Routes[ref]
				if n != ref {
					delete(f.Routes, ref)
				}
				return nil
			}},
		{"capability", fEnum, false, caps,
			func(f *config.File) string {
				if r := find(f); r != nil {
					return string(r.Capability)
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				r := find(f)
				if r == nil {
					return fmt.Errorf("route was removed elsewhere")
				}
				r.Capability = unimatrix.Capability(e.value())
				f.Routes[ref] = *r
				return nil
			}},
		{"needs tools", fEnum, false, []string{"off", "on"},
			func(f *config.File) string {
				if r := find(f); r != nil && r.NeedsTools {
					return "on"
				}
				return "off"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				r := find(f)
				if r == nil {
					return fmt.Errorf("route was removed elsewhere")
				}
				r.NeedsTools = e.value() == "on"
				f.Routes[ref] = *r
				return nil
			}},
		{"max cost $", fText, false, nil,
			func(f *config.File) string {
				if r := find(f); r != nil && r.MaxCost > 0 {
					return fmt.Sprintf("%g", r.MaxCost)
				}
				return "0"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				r := find(f)
				if r == nil {
					return fmt.Errorf("route was removed elsewhere")
				}
				v, err := strconv.ParseFloat(strings.TrimSpace(e.value()), 64)
				if err != nil || v < 0 {
					return fmt.Errorf("max cost must be a number")
				}
				r.MaxCost = v
				f.Routes[ref] = *r
				return nil
			}},
		{"prefer", fText, false, nil,
			func(f *config.File) string {
				if r := find(f); r != nil {
					return strings.Join(r.Prefer, ", ")
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				r := find(f)
				if r == nil {
					return fmt.Errorf("route was removed elsewhere")
				}
				r.Prefer = parseTags(e.value())
				f.Routes[ref] = *r
				return nil
			}},
		{"delete", fEnum, false, []string{"-", "DELETE"},
			func(f *config.File) string { return "-" }, nil,
			func(f *config.File, e *fieldEdit) error {
				if e.value() != "DELETE" {
					return fmt.Errorf("cycle to DELETE and enter to remove this route")
				}
				delete(f.Routes, ref)
				return nil
			}},
	}
}

// --- field definitions -------------------------------------------------------------

// fieldDef describes one editable field. get/set take the target config.File
// explicitly, so commits can apply to a FRESH snapshot instead of a stale
// working copy (the old API captured m.dirty and silently reverted concurrent
// edits — see commitField).
type fieldDef struct {
	label    string
	kind     fieldKind
	password bool
	options  []string
	get      func(f *config.File) string
	getMulti func(f *config.File) []string
	set      func(f *config.File, e *fieldEdit) error
}

// fieldDefs returns the editable fields for the current context: the
// General/Appearance tab's flat list, or the open entity's fields.
func (m SettingsModel) fieldDefs() []fieldDef {
	switch {
	case m.view == viewEntity && m.entityKind == "route":
		return routeFieldDefs(m.entityRef)
	case m.view == viewEntity:
		return m.providerFieldDefs()
	case m.tabKeyOf() == "appearance":
		return m.appearanceFieldDefs()
	default: // general
		return m.generalFieldDefs()
	}
}

// providerIndex finds a provider by name (the entity's stable reference).
func providerIndex(f *config.File, ref string) int {
	for i := range f.Providers {
		if f.Providers[i].Name == ref {
			return i
		}
	}
	return -1
}

// modelIndex finds a model by id (the entity's stable reference).
func modelIndex(f *config.File, ref string) int {
	for i := range f.Models {
		if f.Models[i].ID == ref {
			return i
		}
	}
	return -1
}

// generalFieldDefs: the profile scalars plus the behavior dials (effort is
// the sole reasoning control; the old "(enter cycles)" baked hints are gone —
// these are real enum fields).
func (m SettingsModel) generalFieldDefs() []fieldDef {
	return []fieldDef{
		{"effort", fEnum, false, []string{"off", "low", "medium", "high", "max"},
			func(f *config.File) string { return orDefault(f.Effort, "medium") }, nil,
			func(f *config.File, e *fieldEdit) error { f.Effort = e.value(); return nil }},
		{"max tokens", fEnum, false, []string{"4096", "8192", "16384", "32768"},
			func(f *config.File) string {
				if f.MaxTokens == 0 {
					return "8192"
				}
				return strconv.Itoa(f.MaxTokens)
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				n, err := strconv.Atoi(e.value())
				if err != nil {
					return fmt.Errorf("max tokens must be a number")
				}
				f.MaxTokens = n
				return nil
			}},
		{"temperature", fEnum, false, []string{"0.2", "0.4", "0.7", "1.0"},
			func(f *config.File) string {
				if f.Temperature == 0 {
					return "0.7"
				}
				return fmt.Sprintf("%.1f", f.Temperature)
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				v, err := strconv.ParseFloat(e.value(), 64)
				if err != nil {
					return fmt.Errorf("temperature must be a number")
				}
				f.Temperature = v
				return nil
			}},
		{"language", fEnum, false, []string{"en", "fr", "es", "de", "it", "pt", "zh", "ja", "ko", "ar", "hi"},
			func(f *config.File) string { return orDefault(f.Language, "en") }, nil,
			func(f *config.File, e *fieldEdit) error { f.Language = e.value(); return nil }},
		{"privacy", fEnum, false, []string{"standard", "strict", "local-only"},
			func(f *config.File) string { return orDefault(f.PrivacyLevel, "standard") }, nil,
			func(f *config.File, e *fieldEdit) error { f.PrivacyLevel = e.value(); return nil }},
		{"name", fText, false, nil, func(f *config.File) string { return f.Name }, nil,
			func(f *config.File, e *fieldEdit) error { f.Name = strings.TrimSpace(e.value()); return nil }},
		{"email", fText, false, nil, func(f *config.File) string { return f.Email }, nil,
			func(f *config.File, e *fieldEdit) error { f.Email = strings.TrimSpace(e.value()); return nil }},
		{"domain", fText, false, nil, func(f *config.File) string { return f.Domain }, nil,
			func(f *config.File, e *fieldEdit) error { f.Domain = strings.TrimSpace(e.value()); return nil }},
		{"org", fText, false, nil, func(f *config.File) string { return f.Org }, nil,
			func(f *config.File, e *fieldEdit) error { f.Org = strings.TrimSpace(e.value()); return nil }},
		{"notes", fText, false, nil, func(f *config.File) string { return f.Notes }, nil,
			func(f *config.File, e *fieldEdit) error { f.Notes = strings.TrimSpace(e.value()); return nil }},
	}
}

func (m SettingsModel) providerFieldDefs() []fieldDef {
	return providerFieldDefs(m.entityRef)
}

// providerFieldDefs builds a provider's editable fields keyed by its stable
// name reference (shared with the Models screen's editor).
func providerFieldDefs(ref string) []fieldDef {
	find := func(f *config.File) int { return providerIndex(f, ref) }
	return []fieldDef{
		{"name", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return f.Providers[i].Name
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				n := strings.TrimSpace(e.value())
				if n == "" {
					return fmt.Errorf("name is required")
				}
				for j, op := range f.Providers {
					if j != i && op.Name == n {
						return fmt.Errorf("name %q already used", n)
					}
				}
				old := f.Providers[i].Name
				np := f.Providers[i]
				np.Name = n
				f.Providers[i] = np
				if n != old {
					for j := range f.Models {
						if f.Models[j].Provider == old {
							f.Models[j].Provider = n
						}
					}
				}
				return nil
			}},
		{"base_url", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return f.Providers[i].BaseURL
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				f.Providers[i].BaseURL = strings.TrimSpace(e.value())
				return nil
			}},
		{"api_key", fText, true, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return f.Providers[i].APIKey
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				f.Providers[i].APIKey = strings.TrimSpace(e.value())
				return nil
			}},
		{"wire", fEnum, false, []string{"openai", "anthropic"},
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return f.Providers[i].Wire
				}
				return "openai"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				f.Providers[i].Wire = e.value()
				return nil
			}},
		{"tags", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return strings.Join(f.Providers[i].Tags, ", ")
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				f.Providers[i].Tags = parseTags(e.value())
				return nil
			}},
		// Advanced knobs — consumed by RouteClient + the circuit breakers with
		// no UI before this; 0 = unset (defaults apply).
		{"timeout_ms", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 && f.Providers[i].TimeoutMS > 0 {
					return strconv.Itoa(f.Providers[i].TimeoutMS)
				}
				return "0"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				n, err := strconv.Atoi(strings.TrimSpace(e.value()))
				if err != nil || n < 0 {
					return fmt.Errorf("timeout must be a non-negative number")
				}
				f.Providers[i].TimeoutMS = n
				return nil
			}},
		{"max_failures", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 && f.Providers[i].MaxFailures > 0 {
					return strconv.Itoa(f.Providers[i].MaxFailures)
				}
				return "0"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				n, err := strconv.Atoi(strings.TrimSpace(e.value()))
				if err != nil || n < 0 {
					return fmt.Errorf("max failures must be a non-negative number")
				}
				f.Providers[i].MaxFailures = n
				return nil
			}},
		{"cooldown_ms", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 && f.Providers[i].CooldownMS > 0 {
					return strconv.Itoa(f.Providers[i].CooldownMS)
				}
				return "0"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				n, err := strconv.Atoi(strings.TrimSpace(e.value()))
				if err != nil || n < 0 {
					return fmt.Errorf("cooldown must be a non-negative number")
				}
				f.Providers[i].CooldownMS = n
				return nil
			}},
		{"max_usd", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 && f.Providers[i].MaxUSD > 0 {
					return fmt.Sprintf("%g", f.Providers[i].MaxUSD)
				}
				return "0"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				v, err := strconv.ParseFloat(strings.TrimSpace(e.value()), 64)
				if err != nil || v < 0 {
					return fmt.Errorf("max usd must be a number")
				}
				f.Providers[i].MaxUSD = v
				return nil
			}},
		{"max_tokens", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 && f.Providers[i].MaxTokens > 0 {
					return strconv.Itoa(f.Providers[i].MaxTokens)
				}
				return "0"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				n, err := strconv.Atoi(strings.TrimSpace(e.value()))
				if err != nil || n < 0 {
					return fmt.Errorf("max tokens must be a non-negative number")
				}
				f.Providers[i].MaxTokens = n
				return nil
			}},
		{"clearance", fEnum, false, []string{"public", "internal", "restricted", "secret"},
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return orDefault(f.Providers[i].Clearance, "public")
				}
				return "public"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("provider was removed elsewhere")
				}
				f.Providers[i].Clearance = e.value()
				return nil
			}},
	}
}

func (m SettingsModel) modelFieldDefs() []fieldDef {
	return modelFieldDefs(m.entityRef, m.providerNames())
}

// modelFieldDefs builds a model's editable fields keyed by its stable id
// reference (shared with the Models screen's editor).
func modelFieldDefs(ref string, provNames []string) []fieldDef {
	find := func(f *config.File) int { return modelIndex(f, ref) }
	caps := capStrings()
	return []fieldDef{
		{"id", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return f.Models[i].ID
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("model was removed elsewhere")
				}
				n := strings.TrimSpace(e.value())
				if n == "" {
					return fmt.Errorf("id is required")
				}
				for j, om := range f.Models {
					if j != i && om.ID == n {
						return fmt.Errorf("id %q already used", n)
					}
				}
				old := f.Models[i].ID
				nm := f.Models[i]
				nm.ID = n
				f.Models[i] = nm
				if n != old {
					for role, rid := range f.Roles {
						if rid == old {
							f.Roles[role] = n
						}
					}
				}
				return nil
			}},
		{"label", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return f.Models[i].Label
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("model was removed elsewhere")
				}
				f.Models[i].Label = strings.TrimSpace(e.value())
				return nil
			}},
		{"provider", fEnum, false, provNames,
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return f.Models[i].Provider
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("model was removed elsewhere")
				}
				f.Models[i].Provider = e.value()
				return nil
			}},
		{"capabilities", fMulti, false, caps, func(f *config.File) string { return "" },
			func(f *config.File) []string {
				if i := find(f); i >= 0 {
					return f.Models[i].Caps()
				}
				return nil
			},
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("model was removed elsewhere")
				}
				f.Models[i].Capabilities = stringsToCaps(e.selected())
				return nil
			}},
		{"context", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return strconv.Itoa(f.Models[i].Context)
				}
				return "0"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("model was removed elsewhere")
				}
				n, err := strconv.Atoi(strings.TrimSpace(e.value()))
				if err != nil {
					return fmt.Errorf("context must be a whole number of tokens")
				}
				f.Models[i].Context = n
				return nil
			}},
		{"reasoning_style", fEnum, false, []string{"", "gptoss", "qwen", "gemma4", "deepseek", "anthropic"},
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return f.Models[i].ReasoningStyle
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("model was removed elsewhere")
				}
				f.Models[i].ReasoningStyle = e.value()
				return nil
			}},
		{"effort_override", fEnum, false, []string{"", "off", "low", "medium", "high", "max"},
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return f.Models[i].Effort
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("model was removed elsewhere")
				}
				f.Models[i].Effort = e.value()
				return nil
			}},
		{"tags", fText, false, nil,
			func(f *config.File) string {
				if i := find(f); i >= 0 {
					return strings.Join(f.Models[i].Tags, ", ")
				}
				return ""
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				i := find(f)
				if i < 0 {
					return fmt.Errorf("model was removed elsewhere")
				}
				f.Models[i].Tags = parseTags(e.value())
				return nil
			}},
	}
}

func (m SettingsModel) appearanceFieldDefs() []fieldDef {
	return []fieldDef{
		{"enabled", fEnum, false, []string{"on", "off"},
			func(f *config.File) string {
				ensureStatusLine(f)
				if f.StatusLine.Enabled {
					return "on"
				}
				return "off"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				ensureStatusLine(f)
				f.StatusLine.Enabled = e.value() == "on"
				return nil
			}},
		{"segments", fMulti, false, []string{"cwd", "git", "model", "mode", "tokens", "clock"},
			func(f *config.File) string { return "" },
			func(f *config.File) []string {
				ensureStatusLine(f)
				if len(f.StatusLine.Segments) == 0 {
					return []string{"cwd", "model", "mode", "tokens"}
				}
				return append([]string(nil), f.StatusLine.Segments...)
			},
			func(f *config.File, e *fieldEdit) error {
				ensureStatusLine(f)
				f.StatusLine.Segments = e.selected()
				return nil
			}},
		{"command", fText, false, nil,
			func(f *config.File) string {
				ensureStatusLine(f)
				return f.StatusLine.Command
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				ensureStatusLine(f)
				f.StatusLine.Command = strings.TrimSpace(e.value())
				return nil
			}},
		{"sidebar", fEnum, false, []string{"auto", "on", "off"},
			func(f *config.File) string {
				if f.Appearance != nil && f.Appearance.Sidebar != "" {
					return f.Appearance.Sidebar
				}
				return "auto"
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				ensureAppearance(f)
				f.Appearance.Sidebar = e.value()
				return nil
			}},
		{"top bar legend", fEnum, false, []string{"on", "off"},
			func(f *config.File) string {
				if f.Appearance != nil && f.Appearance.TopBarLegend != nil {
					if *f.Appearance.TopBarLegend {
						return "on"
					}
					return "off"
				}
				return "on" // default when unset
			}, nil,
			func(f *config.File, e *fieldEdit) error {
				ensureAppearance(f)
				v := e.value() == "on"
				f.Appearance.TopBarLegend = &v
				return nil
			}},
	}
}

func ensureStatusLine(f *config.File) {
	if f.StatusLine == nil {
		f.StatusLine = &config.StatusLine{
			Enabled:  true,
			Segments: []string{"cwd", "model", "mode", "tokens"},
		}
	}
}

func ensureAppearance(f *config.File) {
	if f.Appearance == nil {
		f.Appearance = &config.Appearance{}
	}
}

// --- helpers -----------------------------------------------------------------------

func (m SettingsModel) providerNames() []string {
	out := make([]string, len(m.dirty.Providers))
	for i, p := range m.dirty.Providers {
		out[i] = p.Name
	}
	return out
}

func (m SettingsModel) modelExists(id string) bool {
	for _, mo := range m.dirty.Models {
		if mo.ID == id {
			return true
		}
	}
	return false
}

func capStrings() []string {
	caps := unimatrix.Capabilities()
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return out
}

func stringsToCaps(ss []string) []unimatrix.Capability {
	var out []unimatrix.Capability
	for _, s := range ss {
		out = append(out, unimatrix.Capability(s))
	}
	return out
}

func parseTags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func functionKeyLabel(n int) string {
	// Derived from the REAL default bindings — never a second table to drift.
	action := keybindings.DefaultAction(fmt.Sprintf("f%d", n))
	switch action {
	case keybindings.Help:
		return "help"
	case keybindings.Settings:
		return "settings"
	case keybindings.Model:
		return "model chooser"
	case keybindings.Effort:
		return "effort"
	case keybindings.NewChat:
		return "new chat"
	case keybindings.Resume:
		return "resume"
	case keybindings.ContextView:
		return "grid"
	case keybindings.Cron:
		return "cron"
	case keybindings.QueenMode:
		return "mode"
	case keybindings.Sidebar:
		return "sidebar"
	case keybindings.Models:
		return "models"
	case keybindings.Diagnostics:
		return "status"
	}
	return "(free)"
}

// settingRow renders one "key: value" line.
func settingRow(key, val string) string {
	return styleSettingsKey.Render(fmt.Sprintf("%-16s", key)) + styleSettingsVal.Render(val)
}

// modelLabel renders a model as "Label (id)".
func modelLabel(m unimatrix.Model) string {
	if m.Label != "" {
		return fmt.Sprintf("%s (%s)", m.Label, m.ID)
	}
	return m.ID
}
