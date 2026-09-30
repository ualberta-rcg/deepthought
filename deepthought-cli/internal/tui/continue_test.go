package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/history"
)

type listingStore struct {
	history.ChatStore
	sums []history.ChatSummary
}

func (s listingStore) ListCollectives() ([]history.ChatSummary, error) { return s.sums, nil }

func continueFixture(t *testing.T) ContinueModel {
	t.Helper()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	src := listingStore{sums: []history.ChatSummary{
		{ID: "c1", Title: "Slurm array job for genome assembly", UpdatedAt: now, Incursions: 4},
		{ID: "c2", Title: "Proxmox VM template", UpdatedAt: now, Incursions: 1},
		{ID: "c3", Title: "GPU memory profiling", UpdatedAt: now, Incursions: 2},
	}}
	m := NewContinueModel(func() history.ChatStore { return src })
	m.Refresh()
	return m.Resize(80, 16)
}

func TestContinueFrameFitsAndFilters(t *testing.T) {
	m := continueFixture(t)
	for _, ln := range strings.Split(m.View(), "\n") {
		if lipgloss.Width(ln) != 80 {
			t.Fatalf("row width %d: %q", lipgloss.Width(ln), ln)
		}
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !m.CapturingKeys() {
		t.Fatal("/ must start filtering")
	}
	for _, r := range "gpu" {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should resume the filtered chat")
	}
	if got, ok := cmd().(ResumeChatMsg); !ok || got.CollectID != "c3" {
		t.Fatalf("resumed %+v, want c3", got)
	}
}

func TestContinueEmptyState(t *testing.T) {
	m := NewContinueModel(func() history.ChatStore { return listingStore{} })
	m.Refresh()
	m = m.Resize(60, 12)
	if !strings.Contains(stripTestANSI.ReplaceAllString(m.View(), ""), "No saved chats yet") {
		t.Fatal("empty state phrase missing")
	}
}
