package tui

import (
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/tui/kit"
)

// Palette categories, in Tab order. "All" searches every category.
const (
	PaletteAll      = "All"
	PaletteActions  = "Actions"
	PaletteScreens  = "Screens"
	PaletteSettings = "Settings"
	PaletteChats    = "Chats"
	PaletteSlash    = "Slash"
)

var paletteCategories = []string{PaletteAll, PaletteActions, PaletteScreens, PaletteSettings, PaletteChats, PaletteSlash}

// PaletteEntry is one command-palette row. Kind and ID are dispatched as a
// WorkspaceAction, so the palette runs exactly what the menus run.
type PaletteEntry struct {
	Category string
	Label    string
	Detail   string
	Key      string // bound key, shown right-aligned ("" = unbound)
	Kind, ID string
	// Discover entries are listed when the input is empty.
	Discover bool
}

// Ref is the stable identity used for the recents list.
func (e PaletteEntry) Ref() string { return e.Kind + ":" + e.ID }

// PaletteChosenMsg is emitted when an entry is picked.
type PaletteChosenMsg struct{ Entry PaletteEntry }

// PaletteModel is the Ctrl+P command palette overlay: fuzzy search over every
// action, screen, settings section, recent chat and slash command.
type PaletteModel struct {
	entries []PaletteEntry
	recent  map[string]int // ref → rank (0 = most recent)
	cat     int
	list    kit.List
	width   int
	height  int
	done    bool
}

// NewPalette builds the palette. recent lists entry refs, most recent first.
func NewPalette(entries []PaletteEntry, recent []string) PaletteModel {
	p := PaletteModel{entries: entries, recent: map[string]int{}}
	for i, ref := range recent {
		if _, seen := p.recent[ref]; !seen {
			p.recent[ref] = i
		}
	}
	p.rebuild()
	return p
}

// Category is the active category name.
func (p PaletteModel) Category() string { return paletteCategories[p.cat] }

// Query is the typed search text.
func (p PaletteModel) Query() string { return p.list.Filter }

// Selected returns the highlighted entry.
func (p PaletteModel) Selected() (PaletteEntry, bool) {
	it, ok := p.list.Selected()
	if !ok {
		return PaletteEntry{}, false
	}
	e, ok := it.Value.(PaletteEntry)
	return e, ok
}

// rebuild recomputes the candidate rows for the current category and query.
// Recents come first; the list's stable score sort keeps that order on ties.
func (p *PaletteModel) rebuild() {
	cat := p.Category()
	empty := p.list.Filter == ""
	var recent, rest []PaletteEntry
	for _, e := range p.entries {
		if cat != PaletteAll && e.Category != cat {
			continue
		}
		_, isRecent := p.recent[e.Ref()]
		if cat == PaletteAll && empty && !e.Discover && !isRecent {
			continue
		}
		if isRecent {
			recent = append(recent, e)
		} else {
			rest = append(rest, e)
		}
	}
	for i := 1; i < len(recent); i++ {
		for j := i; j > 0 && p.recent[recent[j].Ref()] < p.recent[recent[j-1].Ref()]; j-- {
			recent[j], recent[j-1] = recent[j-1], recent[j]
		}
	}
	items := make([]kit.Item, 0, len(recent)+len(rest))
	for _, e := range append(recent, rest...) {
		detail := e.Detail
		if cat == PaletteAll && detail == "" {
			detail = e.Category
		}
		items = append(items, kit.Item{Label: e.Label, Detail: detail, Key: e.Key, Value: e})
	}
	filter := p.list.Filter
	p.list = kit.List{Items: items, Empty: "Nothing here"}
	p.list.SetFilter(filter)
}

func (p PaletteModel) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch key.String() {
	case "esc":
		if p.list.Filter != "" {
			p.list.SetFilter("")
			p.rebuild()
			return p, nil
		}
		p.done = true
	case "enter":
		e, ok := p.Selected()
		p.done = true
		if ok {
			return p, func() tea.Msg { return PaletteChosenMsg{Entry: e} }
		}
	case "tab":
		p.cat = (p.cat + 1) % len(paletteCategories)
		p.rebuild()
	case "shift+tab":
		p.cat = (p.cat - 1 + len(paletteCategories)) % len(paletteCategories)
		p.rebuild()
	case "up", "ctrl+p":
		p.list.Move(-1)
	case "down", "ctrl+n":
		p.list.Move(1)
	case "backspace":
		if f := []rune(p.list.Filter); len(f) > 0 {
			p.list.SetFilter(string(f[:len(f)-1]))
			p.rebuild()
		}
	case "ctrl+u":
		p.list.SetFilter("")
		p.rebuild()
	default:
		if key.Text != "" {
			p.list.SetFilter(p.list.Filter + key.Text)
			p.rebuild()
		}
	}
	return p, nil
}

func (p PaletteModel) Resize(w, h int) Overlay {
	p.width, p.height = w, h
	return p
}

func (p PaletteModel) Done() bool { return p.done }

func (p PaletteModel) View() string {
	w := min(72, max(24, p.width-4))
	inner := w - 4
	rows := max(3, min(14, p.height-10))
	var tabs []string
	for i, c := range paletteCategories {
		if i == p.cat {
			tabs = append(tabs, styleSettingsTitle.Render(c))
		} else {
			tabs = append(tabs, styleSystem.Render(c))
		}
	}
	tabRow := strings.Join(tabs, styleSystem.Render(" · "))
	if lipgloss.Width(tabRow) > inner {
		tabRow = styleSettingsTitle.Render(p.Category()) + styleSystem.Render("  tab ›")
	}
	query := styleSystem.Render("Type to search")
	if p.list.Filter != "" {
		query = p.list.Filter
	}
	body := []string{"› " + query, tabRow, ""}
	body = append(body, p.list.Render(inner, rows, true)...)
	return kit.Dialog{
		Title: "Command palette",
		Body:  body,
		Help:  []kit.Key{{Key: "enter", Help: "run"}, {Key: "tab", Help: "category"}, {Key: "↑↓", Help: "move"}, {Key: "esc", Help: "close"}},
		Width: w,
	}.Render(p.width, p.height)
}
