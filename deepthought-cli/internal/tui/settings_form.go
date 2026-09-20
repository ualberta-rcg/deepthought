package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbletea/v2"
)

// settings_form.go: an INLINE field editor for the drill-down settings UI.
// One field is open for editing at a time. Three kinds:
//   - text  : a textinput is active; type, enter saves, esc cancels.
//   - enum  : up/down (or ←/→) cycle the value; enter saves, esc cancels.
//   - multi : up/down move, space toggles; enter saves, esc cancels.
//
// "enter saves" is universal, matching the drill-down control scheme. The
// textinput value-receiver gotcha (Update returns a new Model — discard it and
// the keystroke vanishes) is handled by capturing the return.

type fieldKind int

const (
	fText fieldKind = iota
	fEnum
	fMulti
)

// fieldEdit is the active editor for one field.
type fieldEdit struct {
	kind     fieldKind
	label    string
	password bool // text: mask the value
	// text
	input textinput.Model
	// enum / multi
	options []string
	on      map[int]bool // multi: which options are selected
	cur     int          // enum/multi cursor
}

// newTextEdit builds a text-field editor seeded with value.
func newTextEdit(label, value string, password bool) *fieldEdit {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(value)
	ti.Focus()
	return &fieldEdit{kind: fText, label: label, password: password, input: ti}
}

// newEnumEdit builds a single-value cycler seeded with value.
func newEnumEdit(label string, opts []string, value string) *fieldEdit {
	e := &fieldEdit{kind: fEnum, label: label, options: opts}
	for i, o := range opts {
		if o == value {
			e.cur = i
		}
	}
	return e
}

// newMultiEdit builds a multi-toggle seeded with the enabled values.
func newMultiEdit(label string, opts []string, on []string) *fieldEdit {
	e := &fieldEdit{kind: fMulti, label: label, options: opts, on: map[int]bool{}}
	for i, o := range opts {
		for _, v := range on {
			if o == v {
				e.on[i] = true
			}
		}
	}
	return e
}

// value is the current scalar value (text or enum).
func (e *fieldEdit) value() string {
	switch e.kind {
	case fText:
		return e.input.Value()
	case fEnum:
		return e.options[e.cur]
	}
	return ""
}

// selected is the current multi selection as a slice.
func (e *fieldEdit) selected() []string {
	var out []string
	for i, o := range e.options {
		if e.on[i] {
			out = append(out, o)
		}
	}
	return out
}

// update handles a key for the active editor. It returns whether it consumed the
// key (so the caller doesn't also treat it as nav). enter/esc are NOT consumed
// here — the caller interprets enter=save, esc=cancel.
func (e *fieldEdit) update(msg tea.KeyPressMsg) {
	switch e.kind {
	case fText:
		// textinput.Update has a VALUE receiver — capture the return or the
		// keystroke is lost (the original input bug).
		var cmd tea.Cmd
		e.input, cmd = e.input.Update(msg)
		_ = cmd
	case fEnum:
		switch msg.String() {
		case "up", "k", "left", "h":
			e.cur = (e.cur - 1 + len(e.options)) % len(e.options)
		case "down", "j", "right", "l":
			e.cur = (e.cur + 1) % len(e.options)
		}
	case fMulti:
		switch msg.String() {
		case "up", "k", "left", "h":
			e.cur = (e.cur - 1 + len(e.options)) % len(e.options)
		case "down", "j", "right", "l":
			e.cur = (e.cur + 1) % len(e.options)
		case " ", "space": // bubbletea v2 names the key "space" (plain " " never matched)
			e.on[e.cur] = !e.on[e.cur]
		}
	}
}

// setWidth gives the text input room to render.
func (e *fieldEdit) setWidth(w int) {
	if e.kind == fText {
		e.input.SetWidth(w)
	}
}

// view renders the active editor inline.
func (e *fieldEdit) view(width int) string {
	switch e.kind {
	case fText:
		return styleSettingsKey.Render(e.label+": ") + e.input.View()
	case fEnum:
		return styleSettingsKey.Render(e.label+": ") +
			styleSettingsVal.Render("‹ "+e.options[e.cur]+" ›") +
			styleSettingsFoot.Render(fmt.Sprintf("  (↑↓ cycle, enter save)"))
	case fMulti:
		var cells []string
		for i, o := range e.options {
			mark := "○"
			if e.on[i] {
				mark = "●"
			}
			cell := mark + " " + o
			if i == e.cur {
				cell = "[" + cell + "]"
			}
			cells = append(cells, cell)
		}
		return styleSettingsKey.Render(e.label+": ") + styleSettingsVal.Render(strings.Join(cells, "  ")) +
			styleSettingsFoot.Render("  (↑↓ move, space toggle, enter save)")
	}
	return ""
}

// mask returns a masked form of a secret for display (never the real value).
func mask(secret string) string {
	if secret == "" {
		return "(unset)"
	}
	if strings.HasPrefix(secret, "$") {
		return secret // env-var reference is safe to show
	}
	return strings.Repeat("•", 8)
}
