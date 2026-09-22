package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/unimatrix"
)

var stripTestANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func newTestModels() ModelsModel {
	return NewModelsModel(&fakeStore{})
}

// The catalog renders rows + the add affordance, and fits 80 columns.
func TestModelsListRenders(t *testing.T) {
	m := newTestModels().Resize(80, 24)
	v := m.View()
	if v == "" {
		t.Fatal("empty view")
	}
	plain := stripTestANSI.ReplaceAllString(v, "")
	if !strings.Contains(plain, "+ Add from provider") {
		t.Error("missing the add row")
	}
	for i, ln := range strings.Split(v, "\n") {
		if w := lipgloss.Width(ln); w > 80 {
			t.Errorf("line %d width %d > 80", i, w)
		}
	}
}

// Enter drills into the field editor; a commit round-trips through the store
// on a fresh snapshot.
func TestModelsEditRoundTrip(t *testing.T) {
	store := &fakeStore{}
	m := NewModelsModel(store)
	if len(m.dirty.Models) == 0 {
		t.Fatal("test config has no models")
	}
	m.entityRef = m.dirty.Models[0].ID
	m.view = mvEdit
	m.edit = newTextEdit("label", "Renamed Model", false)
	m.editIdx = 1
	m, _ = m.commitField()
	if store.saved == nil {
		t.Fatal("commit did not save")
	}
	for _, mo := range store.saved.Models {
		if mo.ID == m.entityRef && mo.Label == "Renamed Model" {
			return
		}
	}
	t.Error("label not applied via the store round-trip")
}

// Role toggle flips the selected role's assignment through the store, both
// directions.
func TestModelsRoleToggle(t *testing.T) {
	store := &fakeStore{}
	m := NewModelsModel(store)
	m.entityRef = m.dirty.Models[0].ID
	m.view = mvRole
	before := m.dirty.Roles[unimatrix.RoleChat] == m.entityRef

	m, _ = m.toggleRole()
	if store.saved == nil {
		t.Fatal("toggle did not save")
	}
	if after := store.saved.Roles[unimatrix.RoleChat] == m.entityRef; after == before {
		t.Errorf("first toggle did not flip the assignment (before=%v after=%v)", before, after)
	}

	m.dirty = store.Snapshot()
	m, _ = m.toggleRole()
	if after := store.saved.Roles[unimatrix.RoleChat] == m.entityRef; after != before {
		t.Error("second toggle did not flip back")
	}
}

// Deleting a model in use by a role is refused.
func TestModelsDeleteGuard(t *testing.T) {
	store := &fakeStore{}
	m := NewModelsModel(store)
	id := m.dirty.Models[0].ID
	f := store.Snapshot()
	f.Roles = map[string]string{unimatrix.RoleChat: id}
	store.saved = &f
	m.dirty = store.Snapshot()
	m.view, m.cursor = mvList, 0
	m, _ = m.deleteModel()
	if m.saved == "" {
		t.Error("delete of an in-use model should be refused")
	}
	for _, mo := range m.dirty.Models {
		if mo.ID == id && m.saved != "" {
			return // refused and intact
		}
	}
}

// Discovered ids add as chat-capable models; existing ids are not duplicated.
func TestModelsAddFromList(t *testing.T) {
	store := &fakeStore{}
	m := NewModelsModel(store)
	m.view = mvListed
	m.listed = []string{"brand-new-model"}
	m.catalog = []babel.CatalogEntry{{ID: "brand-new-model", Type: "chat"}}
	m.listedProv = m.dirty.Providers[0].Name
	m.cursor = 0
	m, _ = m.addFromList()
	if store.saved == nil {
		t.Fatal("add did not save")
	}
	found := false
	for _, mo := range store.saved.Models {
		if mo.ID == m.listedProv+"::brand-new-model" && mo.RequestID() == "brand-new-model" {
			found = true
			if mo.Provider != m.listedProv || !mo.Can(unimatrix.CapChat) {
				t.Errorf("added model malformed: %+v", mo)
			}
		}
	}
	if !found {
		t.Error("discovered model not added")
	}
	// Adding an existing id is a no-op (no duplicate).
	before := len(store.saved.Models)
	m.dirty = store.Snapshot()
	m.listed = []string{"brand-new-model"}
	m.cursor = 0
	m, _ = m.addFromList()
	if after := len(store.saved.Models); after != before {
		t.Errorf("duplicate added: %d → %d", before, after)
	}
}

// --- field semantics (carried from the old Settings suite) ------------------------

// Editing a model's capabilities lands the enums.
func TestEditModelCapabilities(t *testing.T) {
	m := newTestModels()
	f := m.dirty
	e := newMultiEdit("capabilities", capStrings(), []string{"chat", "reasoning"})
	for _, d := range modelFieldDefs(f.Models[0].ID, m.providerNames()) {
		if d.label == "capabilities" {
			if err := d.set(&f, e); err != nil {
				t.Fatalf("set caps: %v", err)
			}
		}
	}
	mo := f.Models[0]
	if !mo.Can(unimatrix.CapChat) || !mo.Can(unimatrix.CapReasoning) || mo.Can(unimatrix.CapVision) {
		t.Errorf("caps = %v, want chat+reasoning", mo.Capabilities)
	}
}

// Renaming a model rewires roles that referenced it.
func TestEditModelIDRewiresRoles(t *testing.T) {
	m := newTestModels()
	old := m.dirty.Models[0].ID
	f := m.dirty
	f.Roles = map[string]string{unimatrix.RoleChat: old}
	for _, d := range modelFieldDefs(old, m.providerNames()) {
		if d.label == "id" {
			if err := d.set(&f, newTextEdit("id", "renamed-122b", false)); err != nil {
				t.Fatalf("set id: %v", err)
			}
		}
	}
	if f.Models[0].ID != "renamed-122b" {
		t.Errorf("id = %q", f.Models[0].ID)
	}
	if f.Roles[unimatrix.RoleChat] != "renamed-122b" {
		t.Errorf("role not rewired: %q", f.Roles[unimatrix.RoleChat])
	}
}

// Settings is the discoverable entry point to the model catalog.
func TestSettingsHasModelsTab(t *testing.T) {
	for _, tab := range settingsTabs {
		if tab.key == "models" {
			return
		}
	}
	t.Fatal("Settings has no model catalog entry")
}

func TestCatalogIgnoresCanceledAndOlderRequests(t *testing.T) {
	m := newTestModels()
	older, active := new(int), new(int)
	m.catalogRequest = active
	m, _ = m.Update(modelsListedMsg{request: older, provider: "stale", ids: []string{"wrong"}})
	if len(m.listed) != 0 {
		t.Fatal("stale request changed catalog")
	}
	m.catalogRequest = nil
	m, _ = m.Update(modelsListedMsg{request: active, provider: "canceled", ids: []string{"wrong"}})
	if len(m.listed) != 0 {
		t.Fatal("canceled request changed catalog")
	}
}

func TestDiscoveryDoesNotGuessCapabilities(t *testing.T) {
	store := &fakeStore{}
	m := NewModelsModel(store)
	m.listed, m.listedProv = []string{"reasoning-vision-tools-llama"}, m.dirty.Providers[0].Name
	m, _ = m.addFromList()
	last := store.saved.Models[len(store.saved.Models)-1]
	if len(last.Capabilities) != 0 || last.ReasoningStyle != "none" {
		t.Fatalf("guessed capabilities: %+v", last)
	}
}
