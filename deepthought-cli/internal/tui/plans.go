package tui

import (
	tea "charm.land/bubbletea/v2"
	"deepthought-cli/internal/workflow"
	"fmt"
	"strings"
)

type PlansModel struct {
	service                 *workflow.Service
	width, height, selected int
	plans                   []workflow.Plan
	err                     string
}
type plansLoadedMsg struct {
	plans []workflow.Plan
	err   error
}

func NewPlansModel(s *workflow.Service) PlansModel { return PlansModel{service: s} }
func IsPlansEvent(msg tea.Msg) bool                { _, ok := msg.(plansLoadedMsg); return ok }
func (m PlansModel) Resize(w, h int) PlansModel    { m.width, m.height = w, h; return m }
func (m PlansModel) Init() tea.Cmd {
	if m.service == nil {
		return nil
	}
	s := m.service
	return func() tea.Msg { p, e := s.List(); return plansLoadedMsg{p, e} }
}
func (m PlansModel) Update(msg tea.Msg) (PlansModel, tea.Cmd) {
	switch msg := msg.(type) {
	case plansLoadedMsg:
		m.plans = msg.plans
		m.err = ""
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		m.selected = min(m.selected, max(0, len(m.plans)-1))
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			return m, Back()
		case "r":
			return m, m.Init()
		case "up":
			m.selected = max(0, m.selected-1)
		case "down":
			m.selected = min(max(0, len(m.plans)-1), m.selected+1)
		}
	}
	return m, nil
}
func (m PlansModel) View() string {
	rows := []string{"Saved scientific plans · use the workflow tool to create, link jobs and validate results."}
	if m.err != "" {
		rows = append(rows, m.err)
	}
	if len(m.plans) == 0 {
		rows = append(rows, "No saved plans. Ask DeepThought to prepare a plan with explicit success predicates.")
	} else {
		p := m.plans[m.selected]
		rows = append(rows, fmt.Sprintf("%d/%d · %s · %s · version %d", m.selected+1, len(m.plans), p.Directive.Title, p.Directive.Status, p.Directive.Version), "Plan: "+p.Directive.ID)
		for _, o := range p.Directive.Objectives {
			rows = append(rows, fmt.Sprintf("%s · %s · %d attempt(s)", o.Status, o.Description, len(o.Attempts)), "  "+o.ID+" · submission "+p.Submissions[o.ID])
		}
		stale := 0
		for _, a := range p.Artifacts {
			if a.Stale {
				stale++
			}
		}
		rows = append(rows, fmt.Sprintf("Artifacts: %d · stale: %d", len(p.Artifacts), stale))
	}
	return AppScreen(m.width, m.height, screenTitle("Plans"), strings.Join(rows, "\n"), KeyBar([]KeyHint{{Key: "↑/↓", Label: "plan"}, {Key: "r", Label: "refresh"}, {Key: "esc", Label: "back"}}))
}
