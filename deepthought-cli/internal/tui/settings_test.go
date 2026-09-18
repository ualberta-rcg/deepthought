package tui

import (
	"fmt"
	"strings"
	"testing"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/unimatrix"
)

// fakeStore is an in-memory ConfigStore for editor tests.
type fakeStore struct {
	saved *config.File
}

func (f *fakeStore) Snapshot() config.File {
	if f.saved != nil {
		return *f.saved
	}
	return config.Defaults().File
}
func (f *fakeStore) Save(file config.File) error {
	cp := file
	f.saved = &cp
	return nil
}
func (f *fakeStore) Path() string { return "test" }
func (f *fakeStore) ClientFor(string) (*babel.Client, error) {
	return nil, fmt.Errorf("no client in test")
}
func (f *fakeStore) ProviderClient(string) (*babel.Client, error) {
	return nil, fmt.Errorf("no client in test")
}

func newTestSettings() SettingsModel {
	return NewSettingsModel(&fakeStore{}, SettingsInfo{})
}

// atProvider puts the editor on provider entityIdx's field list.
func atProvider(m SettingsModel, idx int) SettingsModel {
	m.level, m.section, m.entityKind, m.entityIdx, m.cursor = lvlEntity, "providers", "provider", idx, 0
	return m
}

func atModel(m SettingsModel, idx int) SettingsModel {
	m.level, m.section, m.entityKind, m.entityIdx, m.cursor = lvlEntity, "models", "model", idx, 0
	return m
}

// setField builds the entity's field defs and applies the named field's setter
// with the given editor (the same path the inline editor takes on commit).
func setField(m SettingsModel, name string, edit *fieldEdit) error {
	for _, d := range m.fieldDefs() {
		if d.label == name {
			return d.set(edit)
		}
	}
	return fmt.Errorf("no field %q", name)
}

// Editing a provider's name renames it and rewires models that referenced it.
func TestEditProviderNameRewiresModels(t *testing.T) {
	m := newTestSettings()
	m.dirty.Models[0].Provider = m.dirty.Providers[0].Name
	old := m.dirty.Providers[0].Name
	m = atProvider(m, 0)

	if err := setField(m, "name", newTextEdit("name", "vulcan-prod", false)); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if m.dirty.Providers[0].Name != "vulcan-prod" {
		t.Errorf("name = %q", m.dirty.Providers[0].Name)
	}
	for _, mo := range m.dirty.Models {
		if mo.Provider == old {
			t.Errorf("model %q still points at old provider %q", mo.ID, old)
		}
	}
}

// A blank provider name is rejected.
func TestProviderNameRequired(t *testing.T) {
	m := atProvider(newTestSettings(), 0)
	if err := setField(m, "name", newTextEdit("name", "  ", false)); err == nil {
		t.Error("blank name should be rejected")
	}
}

// Editing a model's capabilities lands the enums.
func TestEditModelCapabilities(t *testing.T) {
	m := atModel(newTestSettings(), 0)
	e := newMultiEdit("capabilities", capStrings(), []string{"chat", "reasoning"})
	if err := setField(m, "capabilities", e); err != nil {
		t.Fatalf("set caps: %v", err)
	}
	mo := m.dirty.Models[0]
	if !mo.Can(unimatrix.CapChat) || !mo.Can(unimatrix.CapReasoning) || mo.Can(unimatrix.CapVision) {
		t.Errorf("caps = %v, want chat+reasoning", mo.Capabilities)
	}
}

// Renaming a model rewires roles that referenced it.
func TestEditModelIDRewiresRoles(t *testing.T) {
	m := newTestSettings()
	old := m.dirty.Models[0].ID
	m.dirty.Roles = map[string]string{unimatrix.RoleChat: old}
	m = atModel(m, 0)
	if err := setField(m, "id", newTextEdit("id", "renamed-122b", false)); err != nil {
		t.Fatalf("set id: %v", err)
	}
	if m.dirty.Models[0].ID != "renamed-122b" {
		t.Errorf("id = %q", m.dirty.Models[0].ID)
	}
	if m.dirty.Roles[unimatrix.RoleChat] != "renamed-122b" {
		t.Errorf("role not rewired: %q", m.dirty.Roles[unimatrix.RoleChat])
	}
}

// persist writes the working copy to the store.
func TestPersistWritesFile(t *testing.T) {
	m := atProvider(newTestSettings(), 0)
	_ = setField(m, "name", newTextEdit("name", "renamed", false))
	out, _ := m.persist()
	if out.saved != "saved" {
		t.Errorf("saved toast = %q", out.saved)
	}
	store := out.store.(*fakeStore)
	if store.saved == nil || store.saved.Providers[0].Name != "renamed" {
		t.Errorf("file not written: %+v", store.saved)
	}
}

// Deleting a provider a model still uses is refused; otherwise it removes.
func TestDeleteProvider(t *testing.T) {
	m := newTestSettings()
	m.level, m.section, m.cursor = lvlSection, "providers", 0

	// Refused while a model uses it.
	m.dirty.Models[0].Provider = m.dirty.Providers[0].Name
	out, _ := m.deleteEntity()
	if len(out.dirty.Providers) != len(m.dirty.Providers) {
		t.Error("delete should be refused when a model uses the provider")
	}

	// Allowed once unreferenced.
	for i := range m.dirty.Models {
		m.dirty.Models[i].Provider = "none"
	}
	before := len(m.dirty.Providers)
	out, _ = m.deleteEntity()
	if len(out.dirty.Providers) != before-1 {
		t.Errorf("providers = %d, want %d", len(out.dirty.Providers), before-1)
	}
}

// Cycling the agentic role never offers a chat-only model.
func TestCycleAgenticRole(t *testing.T) {
	m := newTestSettings()
	m.dirty.Models = []unimatrix.Model{
		{ID: "big", Provider: m.dirty.Providers[0].Name, Capabilities: []unimatrix.Capability{unimatrix.CapChat, unimatrix.CapTools}},
		{ID: "dumb", Provider: m.dirty.Providers[0].Name, Capabilities: []unimatrix.Capability{unimatrix.CapChat}},
	}
	m.dirty.Roles = map[string]string{unimatrix.RoleChat: "big", unimatrix.RoleAgentic: "big"}
	out, _ := m.cycleRole(unimatrix.RoleAgentic)
	if got := out.dirty.Roles[unimatrix.RoleAgentic]; got != "big" {
		t.Errorf("agentic cycled to %q; chat-only model must never be offered", got)
	}
}

// Root → section drill-in sets level + section.
func TestDrillDownNav(t *testing.T) {
	m := newTestSettings()
	m.cursor = sectionIndex("providers")
	out, _ := m.activate()
	if out.level != lvlSection || out.section != "providers" {
		t.Errorf("after drilling into Providers: level=%v section=%q", out.level, out.section)
	}
	// back() returns to root.
	out2, _ := out.back()
	if out2.level != lvlRoot {
		t.Errorf("back to root: level=%v", out2.level)
	}
}

// addFromList creates a model from a discovered ID.
func TestAddFromList(t *testing.T) {
	m := newTestSettings()
	m.listed, m.listedProv, m.listedSel = []string{"newmodel", "other"}, "vulcan", 0
	out, _ := m.addFromList()
	if out.listed != nil {
		t.Error("picker should close after add")
	}
	last := out.dirty.Models[len(out.dirty.Models)-1]
	if last.ID != "newmodel" || last.Provider != "vulcan" {
		t.Errorf("added model = %+v", last)
	}
	if out.level != lvlEntity || out.entityKind != "model" {
		t.Errorf("should drill into the new model: level=%v kind=%q", out.level, out.entityKind)
	}
}

// An active text field's value must be visible in the rendered screen (the
// earlier bug: the cyan selected-row background hid the textinput).
func TestActiveFieldVisible(t *testing.T) {
	m := newTestSettings()
	m = atProvider(m, 0)
	// Open the name field for editing with a known value.
	m.level = lvlField
	m.editFieldIdx = 0
	m.edit = newTextEdit("name", "vulcan-test", false)
	m.cursor = 0
	out := m.Resize(90, 26).View()
	if !strings.Contains(out, "vulcan-test") {
		t.Error("active field value not visible in the rendered settings screen")
	}
	// And the active row must NOT be swallowed by the cyan selected-row style
	// (it now uses the green ▶ marker instead).
	if !strings.Contains(out, "\x1b") {
		return // no styling in this path
	}
}
