package app

import (
	"testing"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/tui"
)

// stubOverlay is a no-op Overlay for stack-mechanics tests.
type stubOverlay struct{ done bool }

func (s stubOverlay) Update(tea.Msg) (tui.Overlay, tea.Cmd) { return s, nil } //nolint:staticcheck
func (s stubOverlay) View() string                          { return "" }
func (s stubOverlay) Resize(_, _ int) tui.Overlay           { return s }
func (s stubOverlay) Done() bool                            { return s.done }

// TestOverlayStackPushPopNoNilDeref is a regression test for the panic where
// opening a top-level overlay pushed a nil onto the stack and popOverlay then
// dereferenced it. Opening from an empty state must leave the stack empty, and
// popping must clear the overlay (not panic).
func TestOverlayStackPushPopNoNilDeref(t *testing.T) {
	m := &RootModel{width: 80, height: 24}

	// Open a top-level overlay with nothing showing → stack stays empty.
	m.pushOverlay(stubOverlay{})
	if len(m.overlayStack) != 0 {
		t.Fatalf("stack should be empty for a top-level open, got %d", len(m.overlayStack))
	}
	if m.overlay == nil {
		t.Fatal("overlay should be set after push")
	}
	// Pop it → clears, no panic.
	m.popOverlay()
	if m.overlay != nil {
		t.Fatalf("overlay should be nil after pop, got %T", m.overlay)
	}
}

// TestOverlayStackNesting asserts a nested open pushes its parent and pop
// restores it (model chooser → effort → back to chooser).
func TestOverlayStackNesting(t *testing.T) {
	m := &RootModel{width: 80, height: 24}
	parent := stubOverlay{}
	child := stubOverlay{}
	m.pushOverlay(parent)
	m.pushOverlay(child) // nests under parent
	if len(m.overlayStack) != 1 {
		t.Fatalf("expected parent on the stack, got %d", len(m.overlayStack))
	}
	m.popOverlay()
	if _, ok := m.overlay.(stubOverlay); !ok {
		t.Fatalf("expected parent restored after child pop, got %T", m.overlay)
	}
	m.popOverlay()
	if m.overlay != nil {
		t.Fatalf("expected nil after final pop, got %T", m.overlay)
	}
}

// keep babel import used (effort overlay construction references it transitively).
var _ babel.Effort = babel.EffortMedium
