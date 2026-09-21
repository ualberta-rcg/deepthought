package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

// keyPress builds a KeyPressMsg the way the terminal driver does: code +
// printable text (space arrives as Text " " — which String() names "space").
func keyPress(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
}

func newTestSettings() SettingsModel {
	return NewSettingsModel(&fakeStore{}, SettingsInfo{})
}

// atProvider puts the editor on the providers tab, entity idx's field list.
func atProvider(m SettingsModel, idx int) SettingsModel {
	m.tab, m.view = tabIndex("providers"), viewEntity
	m.entityKind, m.entityRef, m.adding, m.cursor = "provider", m.dirty.Providers[idx].Name, false, 0
	return m
}

func tabIndex(key string) int {
	for i, t := range settingsTabs {
		if t.key == key {
			return i
		}
	}
	return 0
}

// setField builds the current field defs and applies the named field's setter
// to f (the same path the inline editor takes on commit).
func setField(m SettingsModel, name string, edit *fieldEdit) error {
	for _, d := range m.fieldDefs() {
		if d.label == name {
			return d.set(&m.dirty, edit)
		}
	}
	return fmt.Errorf("no field %q", name)
}

// --- the seven bug regressions -------------------------------------------------

// Bug 1: the space key toggles a multi-select field. bubbletea v2 names the
// key "space"; the old `case " "` never matched and capabilities could not be
// changed at all.
func TestSpaceTogglesMultiField(t *testing.T) {
	e := newMultiEdit("capabilities", []string{"chat", "reasoning", "vision"}, []string{"chat"})
	if e.on[0] != true {
		t.Fatalf("seed: option 0 should be on")
	}
	e.update(keyPress(tea.KeySpace, " "))
	if e.on[0] != false {
		t.Error("space should have toggled option 0 off")
	}
	e.update(keyPress(tea.KeySpace, " "))
	if e.on[0] != true {
		t.Error("space should have toggled option 0 back on")
	}
	// left/right move the cursor like fEnum.
	e2 := newMultiEdit("segments", []string{"a", "b", "c"}, []string{"a"})
	e2.update(keyPress(tea.KeyRight, ""))
	if e2.cur != 1 {
		t.Errorf("right moved to %d, want 1", e2.cur)
	}
}

// Bug 2: adding a provider is traversable — name → base_url → esc saves; an
// early esc drops the draft without touching the store.
func TestAddProviderDraftFlow(t *testing.T) {
	store := &fakeStore{}
	m := NewSettingsModelAt(store, SettingsInfo{}, "providers", true)
	if m.cursor != len(m.dirty.Providers) {
		t.Fatalf("add cursor = %d, want %d (the + Add row)", m.cursor, len(m.dirty.Providers))
	}
	m, _ = m.activate() // stages the draft
	if !m.adding || m.view != viewEntity {
		t.Fatalf("draft not staged: adding=%v view=%v", m.adding, m.view)
	}

	// Draft commits apply to the working copy only (the store would reject a
	// half-filled provider — the old whole-file validation trap).
	m.edit = newTextEdit("name", "gw", false)
	m.editIdx = 0
	m, _ = m.commitField()
	if store.saved != nil {
		t.Fatal("draft commit must not save yet")
	}
	m.edit = newTextEdit("base_url", "https://gw.local/v1", false)
	m.editIdx = 1
	m, _ = m.commitField()

	// Esc out of the entity view: the now-valid draft is saved.
	m, _ = m.escBack()
	if store.saved == nil {
		t.Fatal("valid draft should have been saved on exit")
	}
	found := false
	for _, p := range store.saved.Providers {
		if p.Name == "gw" {
			found = true
		}
	}
	if !found {
		t.Error("saved file lacks the new provider")
	}
}

func TestAddProviderEarlyEscDropsDraft(t *testing.T) {
	store := &fakeStore{}
	m := NewSettingsModelAt(store, SettingsInfo{}, "providers", true)
	m, _ = m.activate()
	m, _ = m.escBack() // blank draft: dropped, nothing saved
	if store.saved != nil {
		t.Error("blank draft should not be saved")
	}
	for _, p := range m.dirty.Providers {
		if p.Name == "" {
			t.Error("blank draft left in the working copy")
		}
	}
}

// Bug 3: a field commit re-bases on a FRESH snapshot, so concurrent changes
// made elsewhere (effort dial, mode switch, /model) survive the save.
func TestCommitRebasesOnFreshSnapshot(t *testing.T) {
	store := &fakeStore{}
	m := NewSettingsModel(store, SettingsInfo{})
	// An out-of-band write lands after the editor took its snapshot.
	fresh := store.Snapshot()
	fresh.Name = "Rahim"
	store.saved = &fresh

	m, _ = m.gotoTab(tabIndex("general"))
	m.edit = newEnumEdit("effort", []string{"off", "low", "medium", "high", "max"}, "off")
	m.editIdx = 0
	m, _ = m.commitField()

	if store.saved == nil || store.saved.Effort != "off" {
		t.Fatalf("effort not committed: %+v", store.saved)
	}
	if store.saved.Name != "Rahim" {
		t.Error("out-of-band change was clobbered by the commit (stale-snapshot bug)")
	}
}

// Bug 5 (view side): esc walks the ladder field → entity → list → Back.
func TestEscLadder(t *testing.T) {
	m := atProvider(newTestSettings(), 0)
	m, _ = m.Update(keyPress(tea.KeyEnter, "")) // open field 0
	if m.edit == nil {
		t.Fatal("enter should open the field editor")
	}
	m, _ = m.Update(keyPress(tea.KeyEsc, ""))
	if m.edit != nil || m.view != viewEntity {
		t.Fatalf("esc from field: edit=%v view=%v", m.edit != nil, m.view)
	}
	m, _ = m.Update(keyPress(tea.KeyEsc, ""))
	if m.view != viewList {
		t.Fatalf("esc from entity: view=%v, want list", m.view)
	}
	m, cmd := m.Update(keyPress(tea.KeyEsc, ""))
	if cmd == nil {
		t.Error("esc at the tab list should emit Back()")
	}
}

// Tabs switch with ←/→ and wrap; switching resets the cursor and view.
func TestTabSwitching(t *testing.T) {
	m := newTestSettings()
	m, _ = m.Update(keyPress(tea.KeyRight, ""))
	if m.tabKeyOf() != "general" {
		t.Errorf("right → %q, want general", m.tabKeyOf())
	}
	m.cursor = 3
	m, _ = m.Update(keyPress(tea.KeyRight, "")) // → providers
	if m.cursor != 0 {
		t.Error("tab switch should reset the cursor")
	}
	// Wrap from the first tab leftwards to the last.
	m, _ = m.gotoTab(0)
	m, _ = m.Update(keyPress(tea.KeyLeft, ""))
	if m.tab != len(settingsTabs)-1 {
		t.Errorf("left wrap → tab %d, want %d", m.tab, len(settingsTabs)-1)
	}
}

// The rendered editor fits 80 columns exactly (clipLine safety net or not).
func TestSettingsViewFits80(t *testing.T) {
	m := newTestSettings().Resize(80, 24)
	for i, ln := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(ln); w > 80 {
			t.Errorf("line %d width %d > 80: %q", i, w, clipLine(ln, 60))
		}
	}
}

// --- field semantics (carried from the old suite) ---------------------------------

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

// A commit that renames the entity updates the reference this view is keyed
// by, so a follow-up edit still finds the entity.
func TestCommitRenameUpdatesRef(t *testing.T) {
	store := &fakeStore{}
	m := NewSettingsModel(store, SettingsInfo{})
	m = atProvider(m, 0)
	m.edit = newTextEdit("name", "renamed-gw", false)
	m.editIdx = 0
	m, _ = m.commitField()
	if m.entityRef != "renamed-gw" {
		t.Errorf("entityRef = %q, want renamed-gw", m.entityRef)
	}
	// The follow-up edit still resolves.
	m.edit = newTextEdit("base_url", "https://x", false)
	m.editIdx = 1
	m, _ = m.commitField()
	if store.saved == nil {
		t.Fatal("second commit did not save")
	}
	for _, p := range store.saved.Providers {
		if p.Name == "renamed-gw" && p.BaseURL == "https://x" {
			return
		}
	}
	t.Error("renamed provider missing or base_url not applied")
}

// persistRebase writes through the store after validation.
func TestPersistRebaseWritesFile(t *testing.T) {
	store := &fakeStore{}
	m := NewSettingsModel(store, SettingsInfo{})
	out, _ := m.persistRebase(func(f *config.File) { f.Effort = "low" })
	if out.saved != "saved" {
		t.Errorf("saved toast = %q", out.saved)
	}
	if store.saved == nil || store.saved.Effort != "low" {
		t.Error("file not written")
	}
}

// The refill: Overview + Routing tabs render; routes add/edit/delete through
// the store; the provider editor exposes the advanced knobs.
func TestSettingsOverviewAndRouting(t *testing.T) {
	m := newTestSettings().Resize(84, 24)
	m, _ = m.gotoTab(tabIndex("overview"))
	plain := stripTestANSI.ReplaceAllString(m.View(), "")
	for _, want := range []string{"Overview", "valid", "providers", "routes"} {
		if !strings.Contains(plain, want) {
			t.Errorf("overview missing %q", want)
		}
	}

	// Routing: add a route via "+ Add", set a field, save; delete via DELETE.
	m, _ = m.gotoTab(tabIndex("routing"))
	m, _ = m.Update(keyPress(tea.KeyEnter, "")) // "+ Add route" (cursor past keys)
	if !m.adding || m.entityRef != "new-route" {
		t.Fatalf("route draft not staged: adding=%v ref=%q", m.adding, m.entityRef)
	}
	m.edit = newEnumEdit("capability", capStrings(), "embed")
	m.editIdx = 1
	m, _ = m.commitField()
	m, _ = m.escBack() // finishAdd: validates + persists
	store := m.store.(*fakeStore)
	if store.saved == nil {
		t.Fatal("route add did not save")
	}
	if r, ok := store.saved.Routes["new-route"]; !ok || r.Capability != unimatrix.CapEmbed {
		t.Fatalf("route = %+v", store.saved.Routes["new-route"])
	}

	// DELETE field removes it.
	m.dirty = store.Snapshot()
	m.entityKind, m.entityRef, m.adding, m.view = "route", "new-route", false, viewEntity
	m.edit = newEnumEdit("delete", []string{"-", "DELETE"}, "DELETE")
	m.editIdx = 5
	m, _ = m.commitField()
	if _, still := m.dirty.Routes["new-route"]; still {
		t.Error("DELETE should remove the route")
	}
}

// The provider editor exposes the advanced knobs (breaker/budget fields).
func TestProviderAdvancedFields(t *testing.T) {
	m := atProvider(newTestSettings(), 0)
	labels := map[string]bool{}
	for _, d := range m.fieldDefs() {
		labels[d.label] = true
	}
	for _, want := range []string{"timeout_ms", "max_failures", "cooldown_ms", "max_usd", "max_tokens", "clearance"} {
		if !labels[want] {
			t.Errorf("provider editor missing %q", want)
		}
	}
}

// Skills + Tools tabs render the fed data (read-only, empty states included).
func TestSettingsSkillsToolsTabs(t *testing.T) {
	m := newTestSettings().
		SetSkills([]SkillPack{{Name: "alliance-slurm", Desc: "job submission"}, {Name: "alliance-cvmfs", Desc: "software + modules"}}).
		SetTools([]string{"bash", "read", "skill"})
	m = m.Resize(84, 24)
	m, _ = m.gotoTab(tabIndex("skills"))
	plain := stripTestANSI.ReplaceAllString(m.View(), "")
	for _, want := range []string{"Skills", "alliance-slurm", "job submission", "alliance-cvmfs"} {
		if !strings.Contains(plain, want) {
			t.Errorf("skills tab missing %q", want)
		}
	}
	m, _ = m.gotoTab(tabIndex("tools"))
	plain = stripTestANSI.ReplaceAllString(m.View(), "")
	for _, want := range []string{"Tools", "bash", "read", "skill"} {
		if !strings.Contains(plain, want) {
			t.Errorf("tools tab missing %q", want)
		}
	}
	// Empty states.
	empty := newTestSettings().Resize(84, 24)
	e, _ := empty.gotoTab(tabIndex("skills"))
	if !strings.Contains(stripTestANSI.ReplaceAllString(e.View(), ""), "(no skills yet)") {
		t.Error("skills tab lost its empty state")
	}
}
