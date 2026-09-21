package tui

import tea "charm.land/bubbletea/v2"

// Async messages belong to their screen even when it is not visible.
func IsChatEvent(msg tea.Msg) bool {
	switch msg.(type) {
	case chatEvent, streamStartedMsg, streamItemMsg, titleGeneratedMsg, toolResultMsg, inlineBashResultMsg:
		return true
	}
	return false
}

func IsModelsEvent(msg tea.Msg) bool {
	switch msg.(type) {
	case modelTestResultMsg, modelsListedMsg:
		return true
	}
	return false
}

func IsCronEvent(msg tea.Msg) bool {
	switch msg.(type) {
	case cronLoadedMsg, cronAppliedMsg:
		return true
	}
	return false
}

type chatEvent struct {
	owner string
	turn  uint64
	msg   tea.Msg
}

// own binds asynchronous work to the collective and turn that requested it.
func (m ChatModel) own(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	owner, turn := m.coll.ID, m.generation
	return func() tea.Msg { return chatEvent{owner, turn, cmd()} }
}

func (m ChatModel) Stop() ChatModel {
	if m.busy || m.awaiting != nil {
		m, _ = m.interrupt()
	}
	return m
}
