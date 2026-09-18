package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/config"
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

// SettingsInfo is the slim boot-time snapshot the Overview page shows.
type SettingsInfo struct {
	ConfigPath string
	Mode       string
	Addr       string
}

// navLevel is one step in the settings drill-down.
type navLevel int

const (
	lvlRoot    navLevel = iota // section menu (Overview/Providers/Models/Roles/Back)
	lvlSection                 // a section's list (entities, overview rows, roles)
	lvlEntity                  // one entity's field list
	lvlField                   // editing one field inline
)

// rootSection is a top-level settings entry. live=false marks a section that's
// in the tree as a roadmap marker but not yet configurable; selecting it toasts
// "coming soon" instead of drilling in.
type rootSection struct {
	key   string
	label string
	live  bool
}

var rootSections = []rootSection{
	{"overview", "Overview", true},
	{"general", "General", true}, // profile: language/privacy/name/email/domain/org/notes
	{"providers", "Providers", true},
	{"models", "Models", true},
	{"roles", "Roles", true},
	{"behavior", "Behavior", true},
	{"permissions", "Permissions", true},
	{"shell", "Shell & Env", false}, // default shell, extra env
	{"memory", "Memory", false},     // memory dir + recall
	{"skills", "Skills", false},     // enabled skills
	{"tools", "Tools", false},       // enabled tools + per-tool opts
	{"clusters", "Clusters", true},
	{"storage", "Storage", true},
	{"keybindings", "Keybindings", true},
	{"appearance", "Appearance", true}, // status line
	{"privacy", "Privacy", false},       // telemetry opt-out
}

// SettingsModel is the drill-down settings editor. It holds a working copy of
// config.File and a navigation level; every field commit auto-saves to disk.
type SettingsModel struct {
	store ConfigStore
	info  SettingsInfo
	dirty config.File

	level      navLevel
	section    string // current section key (lvlSection+)
	entityKind string // "provider" | "model" (lvlEntity+)
	entityIdx  int    // index into Providers/Models (lvlEntity+)
	adding     bool   // entity was just added via "+ Add"; esc cleans up if blank
	cursor     int    // row cursor in the current list
	saved      string // transient toast

	// field editing (lvlField)
	edit         *fieldEdit
	editFieldIdx int

	// async: model test, list-models picker
	testing    string
	testResult string
	listed     []string
	listedProv string
	listedSel  int

	// Permissions rule list drill-down: "allow" | "ask" | "deny" (empty = top list).
	permBucket string
	permAdding bool // lvlField is appending a new rule to permBucket

	width  int
	height int
}

// NewSettingsModel builds the editor from the live config store + boot info.
func NewSettingsModel(store ConfigStore, info SettingsInfo) SettingsModel {
	m := SettingsModel{store: store, info: info, level: lvlRoot}
	if store != nil {
		m.dirty = store.Snapshot()
	}
	return m
}

// NewSettingsModelAt builds the editor opened directly at a section's list. When
// add is true the cursor lands on the section's "+ Add" row (used by the splash
// to drop the user at Providers › + add when no model is configured). section is
// a rootSection key ("providers", "models", ...).
func NewSettingsModelAt(store ConfigStore, info SettingsInfo, section string, add bool) SettingsModel {
	m := NewSettingsModel(store, info)
	if !sectionLive(section) {
		return m // unknown/non-live section: stay at root
	}
	m.level = lvlSection
	m.section = section
	m.cursor = 0
	if add {
		kind := "provider"
		if section == "models" {
			kind = "model"
		}
		m.cursor = m.entityCount(kind) // the "+ Add" row
	}
	return m
}

// sectionLive reports whether a section key is configurable (not a "coming
// soon" placeholder).
func sectionLive(key string) bool {
	for _, s := range rootSections {
		if s.key == key {
			return s.live
		}
	}
	return false
}

func (m SettingsModel) Init() tea.Cmd { return nil }

// breadcrumb renders the current location.
func (m SettingsModel) breadcrumb() string {
	parts := []string{"Settings"}
	if m.level >= lvlSection && m.section != "" {
		parts = append(parts, sectionLabel(m.section))
	}
	if m.level >= lvlEntity {
		name := m.entityName()
		if m.adding {
			name = "(new)"
		}
		parts = append(parts, name)
	}
	return styleSettingsFoot.Render(strings.Join(parts, " › "))
}

func sectionLabel(key string) string {
	for _, s := range rootSections {
		if s.key == key {
			return s.label
		}
	}
	return key
}

func (m SettingsModel) entityName() string {
	if m.entityKind == "provider" && m.entityIdx >= 0 && m.entityIdx < len(m.dirty.Providers) {
		return m.dirty.Providers[m.entityIdx].Name
	}
	if m.entityKind == "model" && m.entityIdx >= 0 && m.entityIdx < len(m.dirty.Models) {
		return m.dirty.Models[m.entityIdx].ID
	}
	return ""
}

// Update routes keys by navigation level.
func (m SettingsModel) Update(msg tea.Msg) (SettingsModel, tea.Cmd) {
	// Async results from model-test / list-models.
	switch msg := msg.(type) {
	case modelTestResultMsg:
		return m.handleTestResult(msg)
	case modelsListedMsg:
		return m.handleModelsListed(msg)
	}
	// List-models picker overlay.
	if len(m.listed) > 0 {
		return m.updatePicker(msg)
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	// Field editing takes all keys until enter/esc.
	if m.level == lvlField && m.edit != nil {
		return m.updateField(key)
	}
	// Global: esc/left backs out one level (or to menu at root).
	switch key.String() {
	case "esc", "left", "h":
		return m.back()
	case "q":
		// At the settings root, q backs out to the previous screen; deeper levels
		// back out internally.
		if m.level == lvlRoot {
			return m, Back()
		}
		return m.back()
	}
	// Movement + drill-in are uniform across the list levels.
	return m.updateList(key)
}

// back moves up one level (field→entity→section→root→menu).
func (m SettingsModel) back() (SettingsModel, tea.Cmd) {
	switch m.level {
	case lvlField:
		m.cancelField()
	case lvlEntity:
		// If we were adding and the entity is still blank, drop it.
		if m.adding {
			m.dropIfBlank()
		}
		m.level = lvlSection
	case lvlSection:
		if m.section == "permissions" && m.permBucket != "" {
			m.permBucket = ""
			m.permAdding = false
			m.cursor = 0
			m.clampCursor()
			return m, nil
		}
		m.level = lvlRoot
		m.cursor = sectionIndex(m.section)
	case lvlRoot:
		return m, Back()
	}
	m.clampCursor()
	return m, nil
}

func sectionIndex(key string) int {
	for i, s := range rootSections {
		if s.key == key {
			return i
		}
	}
	return 0
}

// updateList handles ↑↓ movement, enter drill-in, and the section action keys
// (d delete, t test, L list) across the list levels.
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
	case "enter", "right", "l":
		return m.activate()
	default:
		// Section-only action keys (d delete, t test, L list). No-op elsewhere.
		return m.updateListAction(key)
	}
	return m, nil
}

// updateListAction handles section action keys (d/t/L); no-op for other keys.
func (m SettingsModel) updateListAction(key tea.KeyPressMsg) (SettingsModel, tea.Cmd) {
	if m.level != lvlSection {
		return m, nil
	}
	switch key.String() {
	case "d":
		return m.deleteEntity()
	case "t":
		return m.testModel()
	case "L":
		return m.listModels()
	}
	return m, nil
}

// activate drills in / opens an edit / toggles, depending on level + cursor.
func (m SettingsModel) activate() (SettingsModel, tea.Cmd) {
	switch m.level {
	case lvlRoot:
		if m.cursor == len(rootSections) { // Back row
			return m, Back()
		}
		s := rootSections[m.cursor]
		if !s.live {
			m.saved = s.label + " — coming soon"
			return m, nil
		}
		m.section = s.key
		m.level = lvlSection
		m.cursor = 0
		return m, nil
	case lvlSection:
		return m.activateSection()
	case lvlEntity:
		return m.openField()
	}
	return m, nil
}

// --- section-level activation ----------------------------------------------

func (m SettingsModel) activateSection() (SettingsModel, tea.Cmd) {
	switch m.section {
	case "overview":
		return m.editOverviewRow()
	case "general":
		return m.openGeneralField()
	case "providers":
		return m.enterOrAddEntity("provider")
	case "models":
		return m.enterOrAddEntity("model")
	case "roles":
		return m.editRole()
	case "behavior":
		return m.editBehavior()
	case "permissions":
		return m.editPermissions()
	case "clusters", "storage", "keybindings", "appearance":
		if m.cursor == m.rowCount()-1 {
			return m.back()
		}
		if m.section == "appearance" {
			return m.editAppearance()
		}
	}
	return m, nil
}

func (m SettingsModel) editBehavior() (SettingsModel, tea.Cmd) {
	switch m.cursor {
	case 0: // effort (the sole reasoning control: off = no thinking)
		order := []string{"off", "low", "medium", "high", "max"}
		at := 0
		for i, value := range order {
			if value == m.dirty.Effort {
				at = i
			}
		}
		m.dirty.Effort = order[(at+1)%len(order)]
	case 1: // max tokens
		order := []int{4096, 8192, 16384, 32768}
		at := 0
		for i, value := range order {
			if value == m.dirty.MaxTokens {
				at = i
			}
		}
		m.dirty.MaxTokens = order[(at+1)%len(order)]
	case 2: // temperature
		order := []float64{0.2, 0.4, 0.7, 1.0}
		at := 0
		for i, value := range order {
			if value == m.dirty.Temperature {
				at = i
			}
		}
		m.dirty.Temperature = order[(at+1)%len(order)]
	case 3:
		return m.back()
	}
	return m.persist()
}

func (m SettingsModel) editAppearance() (SettingsModel, tea.Cmd) {
	defs := m.appearanceFieldDefs()
	n := len(defs)
	if m.cursor >= n { // Back
		return m.back()
	}
	d := defs[m.cursor]
	var edit *fieldEdit
	switch d.kind {
	case fText:
		edit = newTextEdit(d.label, d.get(), d.password)
	case fEnum:
		edit = newEnumEdit(d.label, d.options, d.get())
	case fMulti:
		edit = newMultiEdit(d.label, d.options, d.getMulti())
	}
	edit.setWidth(m.width)
	m.edit = edit
	m.editFieldIdx = m.cursor
	m.level = lvlField
	return m, m.focusCmd()
}

func (m SettingsModel) appearanceFieldDefs() []fieldDef {
	m.ensureStatusLine()
	return []fieldDef{
		{"enabled", fEnum, false, []string{"on", "off"},
			func() string {
				if m.dirty.StatusLine != nil && m.dirty.StatusLine.Enabled {
					return "on"
				}
				return "off"
			}, nil,
			func(e *fieldEdit) error {
				m.ensureStatusLine()
				m.dirty.StatusLine.Enabled = e.value() == "on"
				return nil
			}},
		{"segments", fMulti, false, []string{"cwd", "git", "model", "mode", "tokens", "clock"},
			func() string { return "" },
			func() []string {
				m.ensureStatusLine()
				if len(m.dirty.StatusLine.Segments) == 0 {
					return []string{"cwd", "model", "mode", "tokens"}
				}
				return append([]string(nil), m.dirty.StatusLine.Segments...)
			},
			func(e *fieldEdit) error {
				m.ensureStatusLine()
				m.dirty.StatusLine.Segments = e.selected()
				return nil
			}},
		{"command", fText, false, nil,
			func() string {
				m.ensureStatusLine()
				return m.dirty.StatusLine.Command
			}, nil,
			func(e *fieldEdit) error {
				m.ensureStatusLine()
				m.dirty.StatusLine.Command = strings.TrimSpace(e.value())
				return nil
			}},
	}
}

func (m *SettingsModel) ensureStatusLine() {
	if m.dirty.StatusLine == nil {
		m.dirty.StatusLine = &config.StatusLine{
			Enabled:  true,
			Segments: []string{"cwd", "model", "mode", "tokens"},
		}
	}
}

func (m SettingsModel) editPermissions() (SettingsModel, tea.Cmd) {
	if m.permBucket != "" {
		return m.editPermRules()
	}
	switch m.cursor {
	case 0: // operation mode
		order := []string{"safe", "safe-auto", "auto"}
		current := ""
		if m.dirty.Permissions != nil {
			current = m.dirty.Permissions.Mode
		}
		if current == "" {
			current = m.dirty.PermissionMode
		}
		if current == "" || current == "review" {
			current = "safe"
		}
		if current == "always-proceed" {
			current = "auto"
		}
		at := 0
		for i, value := range order {
			if value == current {
				at = i
			}
		}
		next := order[(at+1)%len(order)]
		if m.dirty.Permissions == nil {
			m.dirty.Permissions = &config.Permissions{}
		}
		m.dirty.Permissions.Mode = next
		m.dirty.PermissionMode = next
		return m.persist()
	case 1:
		m.permBucket, m.cursor = "allow", 0
		return m, nil
	case 2:
		m.permBucket, m.cursor = "ask", 0
		return m, nil
	case 3:
		m.permBucket, m.cursor = "deny", 0
		return m, nil
	case 4:
		return m.back()
	}
	return m, nil
}

// editPermRules handles the allow/ask/deny rule list: enter on a rule deletes
// it, on "+ Add" opens a text editor, on Back clears the bucket.
func (m SettingsModel) editPermRules() (SettingsModel, tea.Cmd) {
	rules := m.permRules()
	n := len(rules)
	switch {
	case m.cursor == n: // + Add
		m.edit = newTextEdit(m.permBucket+" rule", "", false)
		m.edit.setWidth(m.width)
		m.editFieldIdx = -1
		m.permAdding = true
		m.level = lvlField
		return m, m.focusCmd()
	case m.cursor == n+1: // Back
		m.permBucket = ""
		m.cursor = 0
		return m, nil
	default: // delete selected rule
		if m.cursor < 0 || m.cursor >= n {
			return m, nil
		}
		m.setPermRules(append(append([]string{}, rules[:m.cursor]...), rules[m.cursor+1:]...))
		m.saved = "removed rule"
		m.clampCursor()
		return m.persist()
	}
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

func (m *SettingsModel) setPermRules(rules []string) {
	if m.dirty.Permissions == nil {
		m.dirty.Permissions = &config.Permissions{Mode: "safe"}
	}
	switch m.permBucket {
	case "allow":
		m.dirty.Permissions.Allow = rules
	case "ask":
		m.dirty.Permissions.Ask = rules
	case "deny":
		m.dirty.Permissions.Deny = rules
	}
}

// enterOrAddEntity: cursor on an entity row drills into its fields; on the
// "+ Add" row it stages a new entity.
func (m SettingsModel) enterOrAddEntity(kind string) (SettingsModel, tea.Cmd) {
	n := m.entityCount(kind)
	switch {
	case m.cursor == n: // "+ Add"
		if kind == "provider" {
			m.dirty.Providers = append(m.dirty.Providers, config.Provider{Name: "", Wire: "openai"})
			m.entityIdx = len(m.dirty.Providers) - 1
		} else {
			if len(m.dirty.Providers) == 0 {
				m.saved = "add a provider first"
				return m, nil
			}
			m.dirty.Models = append(m.dirty.Models, unimatrix.Model{
				Provider:     m.dirty.Providers[0].Name,
				Capabilities: []unimatrix.Capability{unimatrix.CapChat},
			})
			m.entityIdx = len(m.dirty.Models) - 1
		}
		m.entityKind, m.adding, m.level, m.cursor = kind, true, lvlEntity, 0
		return m, nil
	case m.cursor == n+1: // Back
		m.level = lvlRoot
		m.cursor = sectionIndex(m.section)
		return m, nil
	default:
		m.entityKind, m.entityIdx, m.adding, m.level, m.cursor = kind, m.cursor, false, lvlEntity, 0
		return m, nil
	}
}

func (m SettingsModel) entityCount(kind string) int {
	if kind == "provider" {
		return len(m.dirty.Providers)
	}
	return len(m.dirty.Models)
}

// dropIfBlank removes a just-added entity if the user backed out without filling
// the required field.
func (m *SettingsModel) dropIfBlank() {
	if m.entityKind == "provider" && m.entityIdx < len(m.dirty.Providers) {
		if strings.TrimSpace(m.dirty.Providers[m.entityIdx].Name) == "" {
			m.dirty.Providers = append(m.dirty.Providers[:m.entityIdx], m.dirty.Providers[m.entityIdx+1:]...)
		}
	}
	if m.entityKind == "model" && m.entityIdx < len(m.dirty.Models) {
		if strings.TrimSpace(m.dirty.Models[m.entityIdx].ID) == "" {
			m.dirty.Models = append(m.dirty.Models[:m.entityIdx], m.dirty.Models[m.entityIdx+1:]...)
		}
	}
}

// --- field editing ----------------------------------------------------------

// openField opens the selected entity field for inline editing.
func (m SettingsModel) openField() (SettingsModel, tea.Cmd) {
	defs := m.fieldDefs()
	if m.cursor >= len(defs) { // Back row
		if m.adding {
			m.dropIfBlank()
		}
		m.level = lvlSection
		m.clampCursor()
		return m, nil
	}
	d := defs[m.cursor]
	var edit *fieldEdit
	switch d.kind {
	case fText:
		edit = newTextEdit(d.label, d.get(), d.password)
	case fEnum:
		edit = newEnumEdit(d.label, d.options, d.get())
	case fMulti:
		edit = newMultiEdit(d.label, d.options, d.getMulti())
	}
	edit.setWidth(m.width)
	m.edit = edit
	m.editFieldIdx = m.cursor
	m.level = lvlField
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
		m.cancelField()
		m.level = m.fieldReturnLevel()
		m.clampCursor()
		return m, nil
	}
	m.edit.update(key)
	return m, nil
}

// fieldReturnLevel is where the inline editor returns to on commit/cancel:
// lvlSection for the General profile section (which has no entity layer),
// lvlEntity for provider/model fields.
func (m SettingsModel) fieldReturnLevel() navLevel {
	if m.section == "general" || m.section == "appearance" || m.permBucket != "" {
		return lvlSection
	}
	return lvlEntity
}

// commitField writes the edited value back into the entity, validates, and
// auto-saves to disk.
func (m SettingsModel) commitField() (SettingsModel, tea.Cmd) {
	if m.permAdding && m.permBucket != "" {
		rule := strings.TrimSpace(m.edit.value())
		m.edit = nil
		m.permAdding = false
		m.level = lvlSection
		if rule == "" {
			m.saved = "empty rule ignored"
			return m, nil
		}
		m.setPermRules(append(append([]string{}, m.permRules()...), rule))
		m.saved = "added " + m.permBucket + " rule"
		m.clampCursor()
		return m.persist()
	}
	defs := m.fieldDefs()
	if m.editFieldIdx < 0 || m.editFieldIdx >= len(defs) {
		m.edit = nil
		m.level = m.fieldReturnLevel()
		return m, nil
	}
	d := defs[m.editFieldIdx]
	if err := d.set(m.edit); err != nil {
		m.saved = "✗ " + err.Error()
		return m, nil // stay in the editor so the user can fix it
	}
	// Validate the whole config; if it breaks, stay in the editor.
	if _, err := config.Validate(m.dirty); err != nil {
		m.saved = "✗ " + err.Error()
		return m, nil
	}
	m.edit = nil
	m.level = m.fieldReturnLevel()
	m.adding = false
	m.clampCursor()
	// Auto-save to disk immediately.
	return m.persist()
}

func (m *SettingsModel) cancelField() {
	m.edit = nil
	m.permAdding = false
}

// persist writes the working copy to disk + hot-swaps, then reloads the snapshot.
func (m SettingsModel) persist() (SettingsModel, tea.Cmd) {
	if m.store == nil {
		return m, nil
	}
	if err := m.store.Save(m.dirty); err != nil {
		m.saved = "✗ " + err.Error()
		return m, nil
	}
	m.dirty = m.store.Snapshot()
	m.saved = "saved"
	return m, nil
}

// fieldDef describes one editable field on an entity.
type fieldDef struct {
	label    string
	kind     fieldKind
	password bool
	options  []string
	get      func() string            // scalar value (text/enum)
	getMulti func() []string          // multi value
	set      func(e *fieldEdit) error // write the edit back into the entity
}

// fieldDefs returns the editable fields for the current entity (provider/model),
// or for the General profile section when that's the active section.
func (m SettingsModel) fieldDefs() []fieldDef {
	if m.section == "general" {
		return m.generalFieldDefs()
	}
	if m.section == "appearance" {
		return m.appearanceFieldDefs()
	}
	if m.entityKind == "provider" {
		return m.providerFieldDefs()
	}
	return m.modelFieldDefs()
}

// openGeneralField opens the inline editor for one General/Profile field. The
// General section is a flat field list at lvlSection; activating a row jumps
// straight into lvlField (there is no lvlEntity for it). The Back row backs out.
func (m SettingsModel) openGeneralField() (SettingsModel, tea.Cmd) {
	defs := m.generalFieldDefs()
	if m.cursor < 0 || m.cursor >= len(defs) {
		return m.back() // Back row
	}
	d := defs[m.cursor]
	var edit *fieldEdit
	switch d.kind {
	case fText:
		edit = newTextEdit(d.label, d.get(), d.password)
	case fEnum:
		edit = newEnumEdit(d.label, d.options, d.get())
	case fMulti:
		edit = newMultiEdit(d.label, d.options, d.getMulti())
	}
	edit.setWidth(m.width)
	m.edit = edit
	m.editFieldIdx = m.cursor
	m.level = lvlField
	return m, m.focusCmd()
}

// generalFieldDefs describes the General/Profile scalars (backed by config.File),
// reused by the row renderer, the editor, and commit.
func (m SettingsModel) generalFieldDefs() []fieldDef {
	d := &m.dirty
	return []fieldDef{
		{"language", fEnum, false, []string{"en", "fr", "es", "de", "it", "pt", "zh", "ja", "ko", "ar", "hi"},
			func() string { return orDefault(d.Language, "en") }, nil,
			func(e *fieldEdit) error { d.Language = e.value(); return nil }},
		{"privacy", fEnum, false, []string{"standard", "strict", "local-only"},
			func() string { return orDefault(d.PrivacyLevel, "standard") }, nil,
			func(e *fieldEdit) error { d.PrivacyLevel = e.value(); return nil }},
		{"name", fText, false, nil, func() string { return d.Name }, nil,
			func(e *fieldEdit) error { d.Name = strings.TrimSpace(e.value()); return nil }},
		{"email", fText, false, nil, func() string { return d.Email }, nil,
			func(e *fieldEdit) error { d.Email = strings.TrimSpace(e.value()); return nil }},
		{"domain", fText, false, nil, func() string { return d.Domain }, nil,
			func(e *fieldEdit) error { d.Domain = strings.TrimSpace(e.value()); return nil }},
		{"org", fText, false, nil, func() string { return d.Org }, nil,
			func(e *fieldEdit) error { d.Org = strings.TrimSpace(e.value()); return nil }},
		{"notes", fText, false, nil, func() string { return d.Notes }, nil,
			func(e *fieldEdit) error { d.Notes = strings.TrimSpace(e.value()); return nil }},
	}
}

func (m SettingsModel) providerFieldDefs() []fieldDef {
	p := m.dirty.Providers[m.entityIdx]
	return []fieldDef{
		{"name", fText, false, nil, func() string { return p.Name }, nil,
			func(e *fieldEdit) error {
				n := strings.TrimSpace(e.value())
				if n == "" {
					return fmt.Errorf("name is required")
				}
				for i, op := range m.dirty.Providers {
					if i != m.entityIdx && op.Name == n {
						return fmt.Errorf("name %q already used", n)
					}
				}
				old := m.dirty.Providers[m.entityIdx].Name
				np := m.dirty.Providers[m.entityIdx]
				np.Name = n
				m.dirty.Providers[m.entityIdx] = np
				if n != old {
					for j := range m.dirty.Models {
						if m.dirty.Models[j].Provider == old {
							m.dirty.Models[j].Provider = n
						}
					}
				}
				return nil
			}},
		{"base_url", fText, false, nil, func() string { return m.dirty.Providers[m.entityIdx].BaseURL }, nil,
			func(e *fieldEdit) error {
				m.dirty.Providers[m.entityIdx].BaseURL = strings.TrimSpace(e.value())
				return nil
			}},
		{"api_key", fText, true, nil, func() string { return m.dirty.Providers[m.entityIdx].APIKey }, nil,
			func(e *fieldEdit) error {
				m.dirty.Providers[m.entityIdx].APIKey = strings.TrimSpace(e.value())
				return nil
			}},
		{"wire", fEnum, false, []string{"openai", "anthropic"}, func() string { return m.dirty.Providers[m.entityIdx].Wire }, nil,
			func(e *fieldEdit) error { m.dirty.Providers[m.entityIdx].Wire = e.value(); return nil }},
		{"tags", fText, false, nil, func() string { return strings.Join(m.dirty.Providers[m.entityIdx].Tags, ", ") }, nil,
			func(e *fieldEdit) error { m.dirty.Providers[m.entityIdx].Tags = parseTags(e.value()); return nil }},
	}
}

func (m SettingsModel) modelFieldDefs() []fieldDef {
	mo := m.dirty.Models[m.entityIdx]
	provNames := m.providerNames()
	caps := capStrings()
	return []fieldDef{
		{"id", fText, false, nil, func() string { return mo.ID }, nil,
			func(e *fieldEdit) error {
				n := strings.TrimSpace(e.value())
				if n == "" {
					return fmt.Errorf("id is required")
				}
				for i, om := range m.dirty.Models {
					if i != m.entityIdx && om.ID == n {
						return fmt.Errorf("id %q already used", n)
					}
				}
				old := m.dirty.Models[m.entityIdx].ID
				nm := m.dirty.Models[m.entityIdx]
				nm.ID = n
				m.dirty.Models[m.entityIdx] = nm
				if n != old {
					for role, rid := range m.dirty.Roles {
						if rid == old {
							m.dirty.Roles[role] = n
						}
					}
				}
				return nil
			}},
		{"label", fText, false, nil, func() string { return m.dirty.Models[m.entityIdx].Label }, nil,
			func(e *fieldEdit) error { m.dirty.Models[m.entityIdx].Label = strings.TrimSpace(e.value()); return nil }},
		{"provider", fEnum, false, provNames, func() string { return m.dirty.Models[m.entityIdx].Provider }, nil,
			func(e *fieldEdit) error { m.dirty.Models[m.entityIdx].Provider = e.value(); return nil }},
		{"capabilities", fMulti, false, caps, func() string { return "" }, func() []string { return m.dirty.Models[m.entityIdx].Caps() },
			func(e *fieldEdit) error {
				m.dirty.Models[m.entityIdx].Capabilities = stringsToCaps(e.selected())
				return nil
			}},
		{"context", fText, false, nil, func() string { return strconv.Itoa(m.dirty.Models[m.entityIdx].Context) }, nil,
			func(e *fieldEdit) error { m.dirty.Models[m.entityIdx].Context = atoiOr(e.value(), 0); return nil }},
		{"tags", fText, false, nil, func() string { return strings.Join(m.dirty.Models[m.entityIdx].Tags, ", ") }, nil,
			func(e *fieldEdit) error { m.dirty.Models[m.entityIdx].Tags = parseTags(e.value()); return nil }},
	}
}

// --- overview + roles editing ----------------------------------------------

func (m SettingsModel) editOverviewRow() (SettingsModel, tea.Cmd) {
	// Overview rows: chat model, summary model, back.
	rows := m.overviewRowCount()
	if m.cursor == rows-1 { // Back
		m.level = lvlRoot
		m.cursor = sectionIndex(m.section)
		return m, nil
	}
	switch m.cursor {
	case 0:
		return m.pickRoleModel(unimatrix.RoleChat)
	case 1:
		return m.pickRoleModel(unimatrix.RoleSummary)
	}
	return m, nil
}

func (m SettingsModel) editRole() (SettingsModel, tea.Cmd) {
	roles := unimatrix.Roles()
	if m.cursor == len(roles) { // Back
		m.level = lvlRoot
		m.cursor = sectionIndex(m.section)
		return m, nil
	}
	return m.cycleRole(roles[m.cursor])
}

// cycleRole moves the selected role's model by +1 within its candidate set.
func (m SettingsModel) cycleRole(role string) (SettingsModel, tea.Cmd) {
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
	if m.dirty.Roles == nil {
		m.dirty.Roles = map[string]string{}
	}
	m.dirty.Roles[role] = next.ID
	return m.persist()
}

// pickRoleModel cycles the chat/summary role's model (Overview shortcut).
func (m SettingsModel) pickRoleModel(role string) (SettingsModel, tea.Cmd) {
	return m.cycleRole(role)
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

// --- delete ----------------------------------------------------------------

// deleteEntity removes the selected provider/model (called via a keybar action).
func (m SettingsModel) deleteEntity() (SettingsModel, tea.Cmd) {
	if m.level != lvlSection {
		return m, nil
	}
	if m.section == "providers" {
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
		m.dirty.Providers = append(m.dirty.Providers[:m.cursor], m.dirty.Providers[m.cursor+1:]...)
	} else if m.section == "models" {
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
		m.dirty.Models = append(m.dirty.Models[:m.cursor], m.dirty.Models[m.cursor+1:]...)
	} else {
		return m, nil
	}
	m.clampCursor()
	return m.persist()
}

// --- model test + list models ----------------------------------------------

// testModel fires a tiny chat to verify the selected model works.
func (m SettingsModel) testModel() (SettingsModel, tea.Cmd) {
	if m.section != "models" || m.cursor >= len(m.dirty.Models) {
		return m, nil
	}
	id := m.dirty.Models[m.cursor].ID
	if m.testing != "" {
		return m, nil
	}
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
			Model: id, Messages: []babel.Message{{Role: "user", Content: "Reply with exactly: OK"}}, MaxTokens: 16,
		})
		if err != nil {
			return modelTestResultMsg{modelID: id, latency: time.Since(start), err: err}
		}
		return modelTestResultMsg{modelID: id, ok: true, latency: time.Since(start), text: rep.Text}
	})
}

func (m SettingsModel) handleTestResult(r modelTestResultMsg) (SettingsModel, tea.Cmd) {
	m.testing = ""
	if r.err != nil {
		m.testResult = "✗ " + r.err.Error()
	} else {
		m.testResult = fmt.Sprintf("✓ %s replied in %s", r.modelID, r.latency.Round(time.Millisecond))
	}
	m.saved = m.testResult
	return m, nil
}

// listModels fetches /models from the selected provider.
func (m SettingsModel) listModels() (SettingsModel, tea.Cmd) {
	if m.section != "providers" || m.cursor >= len(m.dirty.Providers) {
		return m, nil
	}
	name := m.dirty.Providers[m.cursor].Name
	m.saved = "listing " + name + "…"
	store := m.store
	return m, tea.Cmd(func() tea.Msg {
		client, err := store.ProviderClient(name)
		if err != nil {
			return modelsListedMsg{provider: name, err: err}
		}
		ids, err := client.ListModels(context.Background())
		return modelsListedMsg{provider: name, ids: ids, err: err}
	})
}

func (m SettingsModel) handleModelsListed(msg modelsListedMsg) (SettingsModel, tea.Cmd) {
	if msg.err != nil {
		m.saved = "✗ " + msg.err.Error()
		m.listed = nil
		return m, nil
	}
	if len(msg.ids) == 0 {
		m.saved = msg.provider + " listed no models"
		return m, nil
	}
	m.listed = msg.ids
	m.listedProv = msg.provider
	m.listedSel = 0
	m.saved = ""
	return m, nil
}

func (m SettingsModel) updatePicker(msg tea.Msg) (SettingsModel, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	n := len(m.listed)
	switch key.String() {
	case "esc", "left", "h":
		m.listed = nil
	case "up", "k":
		m.listedSel = (m.listedSel - 1 + n) % n
	case "down", "j":
		m.listedSel = (m.listedSel + 1) % n
	case "enter", "right", "l":
		return m.addFromList()
	}
	return m, nil
}

// addFromList creates a new model from a discovered ID and drills into it.
func (m SettingsModel) addFromList() (SettingsModel, tea.Cmd) {
	id := m.listed[m.listedSel]
	m.listed = nil
	m.dirty.Models = append(m.dirty.Models, unimatrix.Model{
		ID: id, Label: id, Provider: m.listedProv,
		Capabilities: []unimatrix.Capability{unimatrix.CapChat},
	})
	m.entityKind, m.entityIdx, m.adding, m.level, m.cursor = "model", len(m.dirty.Models)-1, false, lvlEntity, 0
	return m.persist()
}

// --- row model + rendering -------------------------------------------------

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
	err      error
}

// rowCount is the number of navigable rows at the current level.
func (m SettingsModel) rowCount() int {
	switch m.level {
	case lvlRoot:
		return len(rootSections) + 1 // + Back
	case lvlSection:
		switch m.section {
		case "overview":
			return m.overviewRowCount()
		case "providers":
			return len(m.dirty.Providers) + 2 // + Add, Back
		case "models":
			return len(m.dirty.Models) + 2
		case "roles":
			return len(unimatrix.Roles()) + 1 // + Back
		case "behavior":
			return 4
		case "general":
			return len(m.generalFieldDefs()) + 1 // fields + Back
		case "permissions":
			if m.permBucket != "" {
				return len(m.permRules()) + 2 // rules + Add + Back
			}
			return 5 // mode, allow, ask, deny, Back
		case "clusters":
			return 6
		case "storage":
			return 4
		case "keybindings":
			return 13
		case "appearance":
			return len(m.appearanceFieldDefs()) + 1 // fields + Back
		}
	case lvlEntity:
		return len(m.fieldDefs()) + 1 // + Back
	}
	return 0
}

func (m SettingsModel) overviewRowCount() int {
	return 3 // chat model, summary model, Back
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

// Resize stores geometry (the inline editor sizes itself).
func (m SettingsModel) Resize(w, h int) SettingsModel {
	m.width, m.height = w, h
	if m.edit != nil {
		m.edit.setWidth(w)
	}
	return m
}

// rows builds the rendered list for the current level. Each level builds its own
// []string (no shared slice via a closure — that lost the active-editor row to a
// realloc). The field-editor row is swapped in for the active field.
func (m SettingsModel) rows() []string {
	switch m.level {
	case lvlRoot:
		rs := make([]string, 0, len(rootSections)+1)
		for i, s := range rootSections {
			rs = append(rs, m.mark(i, s.label))
		}
		rs = append(rs, m.mark(len(rootSections), "← Back"))
		return rs
	case lvlSection:
		return m.sectionRows()
	case lvlEntity, lvlField:
		if m.permAdding {
			return m.permAddRows()
		}
		return m.entityRows()
	}
	return nil
}

// permAddRows paints the rule list with the inline "+ Add" editor active.
func (m SettingsModel) permAddRows() []string {
	rules := m.permRules()
	rs := make([]string, 0, len(rules)+2)
	for i, r := range rules {
		rs = append(rs, m.mark(i, settingRow(fmt.Sprintf("%d", i+1), r)))
	}
	addIdx := len(rules)
	if m.edit != nil {
		rs = append(rs, styleMenuSel.Render("▶ "+m.edit.view(m.width)))
	} else {
		rs = append(rs, m.mark(addIdx, "+ Add rule"))
	}
	rs = append(rs, m.mark(addIdx+1, "← Back"))
	return rs
}

// mark renders one list row with the cursor highlight.
func (m SettingsModel) mark(i int, text string) string {
	if i == m.cursor {
		return styleMenuSel.Render("▶ " + text)
	}
	return styleMenuUnsel.Render("  " + text)
}

func (m SettingsModel) sectionRows() []string {
	rs := []string{}
	switch m.section {
	case "overview":
		cfg, _ := config.Validate(m.dirty)
		chatS, sumS := "—", "—"
		if cfg != nil {
			if mo, err := cfg.RoleModel(unimatrix.RoleChat); err == nil {
				chatS = modelLabel(mo)
			}
			if mo, err := cfg.RoleModel(unimatrix.RoleSummary); err == nil {
				sumS = modelLabel(mo)
			}
		}
		rs = append(rs, m.mark(0, settingRow("chat model", chatS)))
		rs = append(rs, m.mark(1, settingRow("summary model", sumS)))
		rs = append(rs, m.mark(2, "← Back"))
	case "general":
		d := m.dirty
		rs = append(rs,
			m.mark(0, settingRow("language", orDefault(d.Language, "en"))),
			m.mark(1, settingRow("privacy", orDefault(d.PrivacyLevel, "standard"))),
			m.mark(2, settingRow("name", orDefault(d.Name, "—"))),
			m.mark(3, settingRow("email", orDefault(d.Email, "—"))),
			m.mark(4, settingRow("domain", orDefault(d.Domain, "—"))),
			m.mark(5, settingRow("org", orDefault(d.Org, "—"))),
			m.mark(6, settingRow("notes", orDefault(d.Notes, "—"))),
			m.mark(7, "← Back"),
		)
	case "providers":
		for i, p := range m.dirty.Providers {
			rs = append(rs, m.mark(i, fmt.Sprintf("%-16s %-7s %s", p.Name, p.Wire, strings.Join(p.Tags, ", "))))
		}
		rs = append(rs, m.mark(len(m.dirty.Providers), "+ Add provider"))
		rs = append(rs, m.mark(len(m.dirty.Providers)+1, "← Back"))
	case "models":
		for i, mo := range m.dirty.Models {
			rs = append(rs, m.mark(i, fmt.Sprintf("%-18s %-12s %s", mo.ID, mo.Provider, strings.Join(mo.Caps(), "+"))))
		}
		rs = append(rs, m.mark(len(m.dirty.Models), "+ Add model"))
		rs = append(rs, m.mark(len(m.dirty.Models)+1, "← Back"))
	case "roles":
		cfg, _ := config.Validate(m.dirty)
		for i, role := range unimatrix.Roles() {
			assigned := "—"
			if cfg != nil {
				if mo, err := cfg.RoleModel(role); err == nil {
					assigned = modelLabel(mo)
				}
			}
			rs = append(rs, m.mark(i, settingRow(role, assigned)))
		}
		rs = append(rs, m.mark(len(unimatrix.Roles()), "← Back"))
	case "behavior":
		effort := m.dirty.Effort
		if effort == "" {
			effort = "medium"
		}
		maxTokens := m.dirty.MaxTokens
		if maxTokens == 0 {
			maxTokens = 8192
		}
		temperature := m.dirty.Temperature
		if temperature == 0 {
			temperature = 0.7
		}
		rs = append(rs,
			m.mark(0, settingRow("effort", effort+"   (enter cycles)")),
			m.mark(1, settingRow("max tokens", strconv.Itoa(maxTokens)+"   (enter cycles)")),
			m.mark(2, settingRow("temperature", fmt.Sprintf("%.1f   (enter cycles)", temperature))),
			m.mark(3, "← Back"),
		)
	case "permissions":
		if m.permBucket != "" {
			rules := m.permRules()
			for i, r := range rules {
				rs = append(rs, m.mark(i, settingRow(fmt.Sprintf("%d", i+1), r+"   (enter deletes)")))
			}
			rs = append(rs,
				m.mark(len(rules), "+ Add rule"),
				m.mark(len(rules)+1, "← Back"),
			)
			break
		}
		mode := ""
		if m.dirty.Permissions != nil {
			mode = m.dirty.Permissions.Mode
		}
		if mode == "" {
			mode = m.dirty.PermissionMode
		}
		if mode == "" || mode == "review" {
			mode = "safe"
		}
		if mode == "always-proceed" {
			mode = "auto"
		}
		allowN, askN, denyN := 0, 0, 0
		if m.dirty.Permissions != nil {
			allowN = len(m.dirty.Permissions.Allow)
			askN = len(m.dirty.Permissions.Ask)
			denyN = len(m.dirty.Permissions.Deny)
		}
		rs = append(rs,
			m.mark(0, settingRow("operation mode", mode+"   (enter cycles: safe → safe-auto → auto)")),
			m.mark(1, settingRow("allow rules", fmt.Sprintf("%d   (enter to edit)", allowN))),
			m.mark(2, settingRow("ask rules", fmt.Sprintf("%d   (enter to edit)", askN))),
			m.mark(3, settingRow("deny rules", fmt.Sprintf("%d   (enter to edit)", denyN))),
			m.mark(4, "← Back"),
		)
	case "clusters":
		rs = append(rs,
			m.mark(0, settingRow("scheduler", "Slurm")),
			m.mark(1, settingRow("cluster", envOr("SLURM_CLUSTER_NAME", "local discovery"))),
			m.mark(2, settingRow("partitions / GRES", "machine-readable discovery")),
			m.mark(3, settingRow("fairshare", "sshare + sprio")),
			m.mark(4, settingRow("queue", "squeue --json")),
			m.mark(5, "← Back"),
		)
	case "storage":
		rs = append(rs,
			m.mark(0, settingRow("home", envOr("HOME", "—")+" · backed up")),
			m.mark(1, settingRow("scratch", envOr("SCRATCH", "—")+" · 60-day purge")),
			m.mark(2, settingRow("project", envOr("PROJECT", "—")+" · backed up")),
			m.mark(3, "← Back"),
		)
	case "keybindings":
		for i := 1; i <= 12; i++ {
			rs = append(rs, m.mark(i-1, fmt.Sprintf("F%-2d  %s", i, functionKeyLabel(i))))
		}
		rs = append(rs, m.mark(12, "← Back"))
	case "appearance":
		defs := m.appearanceFieldDefs()
		for i, d := range defs {
			val := ""
			switch d.kind {
			case fMulti:
				val = strings.Join(d.getMulti(), ",")
			default:
				val = d.get()
			}
			if d.label == "command" && val == "" {
				val = "(none)"
			}
			rs = append(rs, m.mark(i, settingRow(d.label, val)))
		}
		rs = append(rs, m.mark(len(defs), "← Back"))
	}
	return rs
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func functionKeyLabel(n int) string {
	labels := []string{"help", "settings", "model", "effort", "new chat", "resume", "context", "stats", "permissions", "", "", "status"}
	if n < 1 || n > len(labels) {
		return ""
	}
	return labels[n-1]
}

func (m SettingsModel) entityRows() []string {
	defs := m.fieldDefs()
	rs := make([]string, 0, len(defs)+1)
	for i, d := range defs {
		if m.level == lvlField && i == m.editFieldIdx && m.edit != nil {
			// Active field: the editor renders distinctly (NOT inside the cyan
			// selected-row style — that made the textinput invisible). Green ▶
			// marker + the editor's own label/value styling on the default bg.
			rs = append(rs, styleEditActive.Render("▶ ")+m.edit.view(m.width))
			continue
		}
		val := d.get()
		if d.password {
			val = mask(val)
		}
		if d.kind == fMulti {
			val = strings.Join(d.getMulti(), "+")
		}
		rs = append(rs, m.mark(i, settingRow(d.label, val)))
	}
	rs = append(rs, m.mark(len(defs), "← Back"))
	return rs
}

// View renders the drill-down as two panes: a read-only section/entity tree on
// the left (a "you are here" map) and the current level's actionable list on the
// right. The level state machine + keys are unchanged; only the layout changes.
func (m SettingsModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	header := m.breadcrumb()
	if m.saved != "" {
		// Toast right-aligned on the header line.
		gap := m.width - 6 - lipgloss.Width(header) - lipgloss.Width(styleToast.Render(m.saved))
		if gap < 1 {
			gap = 1
		}
		header += strings.Repeat(" ", gap) + styleToast.Render(m.saved)
	}
	out := TwoPane(m.width, m.height, header, m.treeView(), strings.Join(m.rows(), "\n"), KeyBar(m.keybar()))
	if len(m.listed) > 0 {
		return overlay(m.width, m.height, out, m.pickerView())
	}
	return out
}

// treeView renders the left pane: the section list, with the current section
// expanded to show its entities, and the current node marked with ◀. Read-only
// context — navigation happens in the right pane.
func (m SettingsModel) treeView() string {
	var rows []string
	for i, s := range rootSections {
		expanded := m.level >= lvlSection && m.section == s.key
		here := m.level == lvlRoot && m.cursor == i
		glyph := "▸"
		if expanded {
			glyph = "▾"
		}
		if here {
			rows = append(rows, styleMenuSel.Render("◀"+glyph+" "+s.label))
		} else if expanded {
			rows = append(rows, styleSettingsVal.Render(" "+glyph+" "+s.label))
		} else {
			rows = append(rows, styleMenuUnsel.Render(" "+glyph+" "+s.label))
		}
		if expanded {
			for j, name := range m.treeEntities(s.key) {
				cur := m.level >= lvlEntity && m.entityKind == entityKindFor(s.key) && m.entityIdx == j
				if cur {
					rows = append(rows, styleMenuSel.Render("◀  ● "+name))
				} else {
					rows = append(rows, styleMenuUnsel.Render("   ● "+name))
				}
			}
		}
	}
	return strings.Join(rows, "\n")
}

// treeEntities returns the entity names to list under a section when expanded.
func (m SettingsModel) treeEntities(section string) []string {
	switch section {
	case "providers":
		out := make([]string, len(m.dirty.Providers))
		for i, p := range m.dirty.Providers {
			out[i] = p.Name
		}
		return out
	case "models":
		out := make([]string, len(m.dirty.Models))
		for i, mo := range m.dirty.Models {
			out[i] = mo.ID
		}
		return out
	}
	return nil
}

// entityKindFor maps a section key to its entity kind.
func entityKindFor(section string) string {
	switch section {
	case "providers":
		return "provider"
	case "models":
		return "model"
	}
	return ""
}

func (m SettingsModel) pickerView() string {
	rows := []string{styleSettingsTitle.Render("Models on " + m.listedProv), ""}
	for i, id := range m.listed {
		mark := " "
		if m.modelExists(id) {
			mark = "✓"
		}
		line := mark + " " + id
		if i == m.listedSel {
			line = styleMenuSel.Render("▶ " + line)
		} else {
			line = styleMenuUnsel.Render("  " + line)
		}
		rows = append(rows, line)
	}
	rows = append(rows, "", styleSettingsFoot.Render("↑↓ select · enter add · esc close"))
	return styleFormBox.Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
}

// keybar returns the per-level keybinding hints.
func (m SettingsModel) keybar() []KeyHint {
	if len(m.listed) > 0 {
		return []KeyHint{{"↑↓", "select"}, {"enter", "add"}, {"esc", "close"}}
	}
	if m.level == lvlField {
		if m.edit != nil && m.edit.kind == fMulti {
			return []KeyHint{{"↑↓", "move"}, {"space", "toggle"}, {"enter", "save"}, {"esc", "cancel"}}
		}
		return []KeyHint{{"enter", "save"}, {"esc", "cancel"}}
	}
	hints := []KeyHint{{"↑↓", "move"}, {"enter", "open"}}
	if m.level == lvlSection {
		switch m.section {
		case "providers":
			hints = append(hints, KeyHint{"d", "delete"}, KeyHint{"L", "list models"})
		case "models":
			hints = append(hints, KeyHint{"d", "delete"}, KeyHint{"t", "test"})
		}
	}
	hints = append(hints, KeyHint{"esc", "back"})
	return hints
}

// --- helpers ----------------------------------------------------------------

func (m SettingsModel) providerNames() []string {
	out := make([]string, len(m.dirty.Providers))
	for i, p := range m.dirty.Providers {
		out[i] = p.Name
	}
	return out
}

func (m SettingsModel) providerExists(name string) bool {
	for _, p := range m.dirty.Providers {
		if p.Name == name {
			return true
		}
	}
	return false
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

func onOff(b *bool) string {
	if b == nil || *b {
		return "on"
	}
	return "off"
}

// overlay centers child over the parent block.
func overlay(w, h int, parent, child string) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, child)
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
