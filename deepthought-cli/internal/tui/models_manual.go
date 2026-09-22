package tui

import (
	"charm.land/bubbletea/v2"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/unimatrix"
	"strings"
)

func (m ModelsModel) manualEntry(provider string) (ModelsModel, tea.Cmd) {
	m.view, m.listedProv, m.saved = mvManual, provider, ""
	m.edit = newTextEdit("model ID", "", false)
	m.edit.setWidth(max(10, m.width-16))
	return m, m.focusCmd()
}

func (m ModelsModel) updateManual(key tea.KeyPressMsg) (ModelsModel, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.edit, m.manual, m.view = nil, false, mvList
		return m, nil
	case "enter":
		wireID := strings.TrimSpace(m.edit.value())
		if wireID == "" || len(wireID) > 512 || strings.ContainsAny(wireID, "\x1b\r\n\x00") {
			m.saved = "Enter a model ID (at most 512 characters)"
			return m, nil
		}
		id := m.listedProv + "::" + wireID
		m, _ = m.freshSave(func(f *config.File) {
			for _, old := range f.Models {
				if old.ID == id {
					return
				}
			}
			f.Models = append(f.Models, unimatrix.Model{ID: id, WireID: wireID, Label: wireID, Provider: m.listedProv, ReasoningStyle: "none"})
		})
		if m.saved != "saved" {
			return m, nil
		}
		m.edit, m.manual, m.view, m.entityRef, m.cursor = nil, false, mvEdit, id, 0
		m.saved = "Added. Set capabilities here; select the model in Settings → Roles."
		return m, nil
	default:
		m.edit.update(key)
		return m, nil
	}
}
