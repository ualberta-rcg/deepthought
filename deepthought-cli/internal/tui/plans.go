package tui

import (
	"fmt"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/tui/kit"
	"deepthought-cli/internal/workflow"
)

type PlansModel struct {
	service       *workflow.Service
	width, height int
	plans         []workflow.Plan
	list          kit.List
	nav           listNav
	err           string
}
type plansLoadedMsg struct {
	plans []workflow.Plan
	err   error
}

const plansEmpty = "No saved plans yet"

func NewPlansModel(s *workflow.Service) PlansModel {
	return PlansModel{service: s, list: kit.List{Empty: plansEmpty}}
}
func IsPlansEvent(msg tea.Msg) bool             { _, ok := msg.(plansLoadedMsg); return ok }
func (m PlansModel) Resize(w, h int) PlansModel { m.width, m.height = w, h; return m }

// CapturingKeys reports whether typed letters belong to the filter.
func (m PlansModel) CapturingKeys() bool { return m.nav.filtering }

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
		items := make([]kit.Item, 0, len(m.plans))
		for i, p := range m.plans {
			if p.Directive == nil {
				continue
			}
			items = append(items, kit.Item{
				Label:  p.Directive.Title,
				Detail: fmt.Sprintf("%s · version %d · %d objectives", p.Directive.Status, p.Directive.Version, len(p.Directive.Objectives)),
				Value:  i,
			})
		}
		m.list.Items = items
		m.list.SetFilter(m.list.Filter)
	case tea.KeyPressMsg:
		if m.nav.key(&m.list, msg) {
			return m, nil
		}
		switch msg.String() {
		case "esc":
			return m, Back()
		case kit.Mnemonics.Refresh:
			return m, m.Init()
		}
	}
	return m, nil
}

func (m PlansModel) View() string {
	notice := ""
	if len(m.list.Items) == 0 {
		notice = "Ask DeepThought to prepare a plan with explicit success predicates."
	}
	if m.err != "" {
		notice = kit.G().Warn + " " + m.err
	}
	var detail *kit.Panel
	if it, ok := m.list.Selected(); ok {
		p := m.plans[it.Value.(int)]
		var body []string
		for _, o := range p.Directive.Objectives {
			line := fmt.Sprintf(" %s · %s · %d attempt(s)", o.Status, o.Description, len(o.Attempts))
			if sub := p.Submissions[o.ID]; sub != "" {
				line += " · submission " + sub
			}
			body = append(body, line)
		}
		if len(body) == 0 {
			body = []string{" No objectives yet."}
		}
		stale := 0
		for _, a := range p.Artifacts {
			if a.Stale {
				stale++
			}
		}
		body = append(body, fmt.Sprintf(" Artifacts: %d · stale: %d", len(p.Artifacts), stale))
		detail = &kit.Panel{
			Title:    p.Directive.Title,
			Status:   p.Directive.ID,
			Body:     body,
			Footnote: " workflow tool: create plans, link jobs, validate results",
		}
	}
	return renderListScreen(m.width, m.height, fmt.Sprintf("Plans · %d saved", len(m.list.Items)), notice, m.list, m.nav, detail,
		m.nav.keys(kit.Key{Key: kit.Mnemonics.Refresh, Help: "refresh"}))
}
