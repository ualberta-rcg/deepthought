package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func paletteFixture() []PaletteEntry {
	return []PaletteEntry{
		{Category: PaletteActions, Label: "New chat", Key: "F5", Kind: "action", ID: "app:new-chat", Discover: true},
		{Category: PaletteActions, Label: "Switch active model", Key: "F3", Kind: "action", ID: "app:model", Discover: true},
		{Category: PaletteActions, Label: "Permission mode", Key: "F9", Kind: "action", ID: "app:queen-mode"},
		{Category: PaletteScreens, Label: "Jobs", Kind: "screen", ID: "7"},
		{Category: PaletteSettings, Label: "Settings › Providers", Kind: "settings-tab", ID: "providers"},
		{Category: PaletteChats, Label: "Slurm array debugging", Kind: "resume", ID: "c1"},
		{Category: PaletteSlash, Label: "/doctor", Kind: "slash", ID: "doctor"},
	}
}

func typeText(p Overlay, s string) Overlay {
	for _, r := range s {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return p
}

func paletteLabels(p PaletteModel) []string {
	var out []string
	for _, i := range p.list.Visible() {
		out = append(out, p.list.Items[i].Label)
	}
	return out
}

func TestPaletteEmptyShowsDiscoverAndRecents(t *testing.T) {
	p := NewPalette(paletteFixture(), []string{"slash:doctor"})
	got := paletteLabels(p)
	want := []string{"/doctor", "New chat", "Switch active model"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("empty palette = %v, want %v", got, want)
	}
}

func TestPaletteFuzzySearchesEverything(t *testing.T) {
	var o Overlay = NewPalette(paletteFixture(), nil)
	o = typeText(o, "slrm")
	e, ok := o.(PaletteModel).Selected()
	if !ok || e.ID != "c1" {
		t.Fatalf("fuzzy pick = %+v, want the Slurm chat", e)
	}
}

func TestPaletteTabCyclesCategories(t *testing.T) {
	var o Overlay = NewPalette(paletteFixture(), nil)
	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	p := o.(PaletteModel)
	if p.Category() != PaletteActions || len(paletteLabels(p)) != 3 {
		t.Fatalf("tab → %s %v", p.Category(), paletteLabels(p))
	}
	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if got := o.(PaletteModel).Category(); got != PaletteSlash {
		t.Fatalf("shift+tab wraps to %s, want Slash", got)
	}
}

func TestPaletteEnterEmitsChoice(t *testing.T) {
	var o Overlay = NewPalette(paletteFixture(), nil)
	o = typeText(o, "perm")
	o, cmd := o.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !o.Done() || cmd == nil {
		t.Fatal("enter must close the palette and emit a choice")
	}
	msg, ok := cmd().(PaletteChosenMsg)
	if !ok || msg.Entry.ID != "app:queen-mode" {
		t.Fatalf("chosen = %+v", msg)
	}
}

func TestPaletteEscClearsThenCloses(t *testing.T) {
	var o Overlay = NewPalette(paletteFixture(), nil)
	o = typeText(o, "jo")
	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if o.Done() || o.(PaletteModel).Query() != "" {
		t.Fatal("first esc clears the query")
	}
	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !o.Done() {
		t.Fatal("second esc closes")
	}
}

func TestPaletteFitsSmallTerminals(t *testing.T) {
	for _, size := range [][2]int{{30, 12}, {80, 24}, {200, 60}} {
		v := NewPalette(paletteFixture(), nil).Resize(size[0], size[1]).View()
		lines := strings.Split(v, "\n")
		if len(lines) > size[1] {
			t.Fatalf("%dx%d: %d rows", size[0], size[1], len(lines))
		}
		for _, ln := range lines {
			if lipgloss.Width(ln) > size[0] {
				t.Fatalf("%dx%d: row %q is %d wide", size[0], size[1], ln, lipgloss.Width(ln))
			}
		}
	}
}
