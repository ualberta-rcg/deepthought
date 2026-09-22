package tui

import (
	"time"

	"charm.land/bubbletea/v2"
)

// Screen identifies a top-level screen. Only the root model (package app)
// switches on it; sub-models request transitions by returning Goto(...).
type Screen int

const (
	ScreenSplash Screen = iota
	ScreenChat
	ScreenSettings
	ScreenModels
	ScreenCron
	ScreenContinue
	ScreenGrid
	ScreenStatus
	ScreenJobs
	ScreenPlans
	ScreenWorkspace
)

// SplashAdvanceMsg is emitted by the splash on any keypress. The root decides
// where to go: a working agentic model → straight into a new chat; otherwise →
// Settings at the add-provider area. The splash itself stays dumb.
type SplashAdvanceMsg struct{}

// SplashServerLoginMsg is emitted when the user picks "Log in to server" on
// the splash. The root runs the async login (app.ServerLoginResultMsg).
type SplashServerLoginMsg struct{}

// ScreenChangeMsg requests a screen transition. Produced by Goto; consumed by
// the root.
type ScreenChangeMsg struct{ To Screen }

// HoldDoneMsg is fired by HoldFor when its delay elapses. The root routes it to
// whichever sub-model is active. Sub-models MUST ignore it when they did not
// arm a timer, so a stale timer from an earlier screen does no damage.
type HoldDoneMsg struct{}

// Goto returns a Cmd requesting a transition to s.
func Goto(s Screen) tea.Cmd {
	return func() tea.Msg { return ScreenChangeMsg{To: s} }
}

// BackMsg asks the root to pop the screen history stack one level (esc = back).
// At the root screen (chat) it is a no-op. Overlays are separate — an open
// overlay's esc pops the overlay before any BackMsg is emitted.
type BackMsg struct{}

// Back returns a Cmd that emits BackMsg.
func Back() tea.Cmd {
	return func() tea.Msg { return BackMsg{} }
}

// HoldFor returns a Cmd that fires HoldDoneMsg once after d. (tea.Tick fires
// a single time, unlike tea.Every.)
func HoldFor(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return HoldDoneMsg{} })
}

// ResumeChatMsg asks the root to rebuild the chat model around an existing
// collective and switch to the chat screen. Emitted by the Continue screen.
type ResumeChatMsg struct{ CollectID string }
