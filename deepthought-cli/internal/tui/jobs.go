package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"deepthought-cli/internal/slurm"
	"fmt"
	"strings"
	"time"
)

type JobsModel struct {
	client        *slurm.Client
	width, height int
	items         []slurm.Submission
	err           string
	loading       bool
	selected      int
}
type jobsLoadedMsg struct {
	items []slurm.Submission
	err   error
}

func NewJobsModel(client *slurm.Client) JobsModel { return JobsModel{client: client} }
func IsJobsEvent(msg tea.Msg) bool                { _, ok := msg.(jobsLoadedMsg); return ok }
func (m JobsModel) Resize(w, h int) JobsModel     { m.width, m.height = w, h; return m }
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
		if m.selected >= len(m.items) {
			m.selected = max(0, len(m.items)-1)
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			return m, Back()
		case "r":
			if !m.loading {
				m.loading = true
				return m, m.Init()
			}
		case "up":
			m.selected = max(0, m.selected-1)
		case "down":
			m.selected = min(max(0, len(m.items)-1), m.selected+1)
		}
	}
	return m, nil
}
func (m JobsModel) View() string {
	var rows []string
	if m.err != "" {
		rows = append(rows, "Refresh failed; showing persisted state: "+m.err)
	}
	if len(m.items) == 0 {
		rows = append(rows, "No journaled submissions. Ask DeepThought to prepare a job; submission requires your approval.")
	}
	start := max(0, m.selected-max(1, m.height-12)/2)
	for i := start; i < len(m.items) && i < start+max(1, m.height-12); i++ {
		s := m.items[i]
		prefix := "  "
		if i == m.selected {
			prefix = "› "
		}
		rows = append(rows, fmt.Sprintf("%s%s · job %s · %s · %s", prefix, s.ID, s.JobID, s.State, s.Updated.Format("Jan 02 15:04")))
	}
	if len(m.items) > 0 {
		item := m.items[m.selected]
		rows = append(rows, "", slurm.RetryAdvice(item), "Script SHA256: "+item.ScriptHash, "Use slurm_log_tail for bounded logs; slurm_cancel checks ownership and permission.")
	}
	return AppScreen(m.width, m.height, screenTitle("Jobs"), strings.Join(rows, "\n"), KeyBar([]KeyHint{{Key: "r", Label: "refresh"}, {Key: "↑/↓", Label: "select"}, {Key: "esc", Label: "back"}}))
}
