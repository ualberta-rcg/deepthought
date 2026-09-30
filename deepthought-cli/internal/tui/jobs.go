package tui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tui/kit"
)

type JobsModel struct {
	client        *slurm.Client
	width, height int
	items         []slurm.Submission
	list          kit.List
	nav           listNav
	err           string
	loading       bool
}
type jobsLoadedMsg struct {
	items []slurm.Submission
	err   error
}

const jobsEmpty = "No journaled submissions yet"

func NewJobsModel(client *slurm.Client) JobsModel {
	return JobsModel{client: client, list: kit.List{Empty: jobsEmpty}}
}
func IsJobsEvent(msg tea.Msg) bool            { _, ok := msg.(jobsLoadedMsg); return ok }
func (m JobsModel) Resize(w, h int) JobsModel { m.width, m.height = w, h; return m }

// CapturingKeys reports whether typed letters belong to the filter.
func (m JobsModel) CapturingKeys() bool { return m.nav.filtering }

func (m JobsModel) Init() tea.Cmd {
	if m.client == nil {
		return nil
	}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		items, err := client.Reconcile(ctx)
		return jobsLoadedMsg{items, err}
	}
}

func (m JobsModel) Update(msg tea.Msg) (JobsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case jobsLoadedMsg:
		m.loading = false
		m.items = msg.items
		m.err = ""
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		items := make([]kit.Item, len(m.items))
		for i, s := range m.items {
			job := "not submitted"
			if s.JobID != "" {
				job = "job " + s.JobID
			}
			items[i] = kit.Item{Label: s.ID, Detail: job + " · " + s.State + " · " + s.Updated.Format("Jan 02 15:04"), Value: i}
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
			if !m.loading {
				m.loading = true
				return m, m.Init()
			}
		}
	}
	return m, nil
}

func (m JobsModel) selected() (slurm.Submission, bool) {
	it, ok := m.list.Selected()
	if !ok {
		return slurm.Submission{}, false
	}
	return m.items[it.Value.(int)], true
}

func (m JobsModel) View() string {
	notice := ""
	if len(m.list.Items) == 0 {
		notice = "Ask DeepThought to prepare a job; submitting it needs your approval."
	}
	if m.err != "" {
		notice = kit.G().Warn + " Refresh failed; showing persisted state: " + m.err
	}
	var detail *kit.Panel
	if s, ok := m.selected(); ok {
		body := []string{" " + slurm.RetryAdvice(s)}
		if s.LastError != "" {
			body = append(body, " Last error: "+s.LastError)
		}
		body = append(body, " Script SHA256: "+s.ScriptHash)
		detail = &kit.Panel{
			Title:    "Submission " + s.ID,
			Status:   s.State,
			Body:     body,
			Footnote: " slurm_log_tail for bounded logs · slurm_cancel checks ownership",
		}
	}
	status := fmt.Sprintf("%d submissions", len(m.items))
	if m.loading {
		status = "refreshing…"
	}
	return renderListScreen(m.width, m.height, "Jobs · "+status, notice, m.list, m.nav, detail,
		m.nav.keys(kit.Key{Key: kit.Mnemonics.Refresh, Help: "refresh"}))
}
