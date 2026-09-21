package app

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/tui"
	"fmt"
	"io"
	"sync"
	"time"
)

// SessionRunner owns the same RootModel and commands used by standalone mode.
// Subscribers receive replaceable screen snapshots; execution and approval state
// live here, independent of any terminal attachment.
type SessionRunner struct {
	ID          string
	program     *tea.Program
	cancel      context.CancelFunc
	done        chan struct{}
	mu          sync.Mutex
	frame       RunnerFrame
	subscribers map[chan RunnerFrame]bool
	journal     *history.SQLiteStore
	state       ResidentState
}
type RunnerFrame struct {
	CursorX       int    `json:"cursor_x,omitempty"`
	CursorY       int    `json:"cursor_y,omitempty"`
	CursorVisible bool   `json:"cursor_visible,omitempty"`
	Sequence      uint64 `json:"sequence"`
	View          string `json:"view"`
	CollectiveID  string `json:"collective_id"`
	Busy          bool   `json:"busy"`
	ApprovalID    string `json:"approval_id,omitempty"`
	Closed        bool   `json:"closed,omitempty"`
}
type RunnerInput struct {
	Paste      *string          `json:"paste,omitempty"`
	Key        *tea.KeyPressMsg `json:"key,omitempty"`
	Width      int              `json:"width,omitempty"`
	Height     int              `json:"height,omitempty"`
	ApprovalID string           `json:"approval_id,omitempty"`
}
type runnerInputMsg struct{ input RunnerInput }
type ResidentState struct {
	ID           string    `json:"id"`
	CollectiveID string    `json:"collective_id"`
	State        string    `json:"state"`
	Updated      time.Time `json:"updated"`
}

func NewSessionRunner(parent context.Context, id string, deps Deps, journal *history.SQLiteStore, resume string) (*SessionRunner, error) {
	ctx, cancel := context.WithCancel(parent)
	r := &SessionRunner{ID: id, cancel: cancel, done: make(chan struct{}), subscribers: map[chan RunnerFrame]bool{}, journal: journal}
	deps.Context, deps.SessionID, deps.ResumeCollective = ctx, id, resume
	deps.Registry = deps.Registry.Fork()
	deps.Gate = deps.Gate.Clone()
	deps.StartScreen = tui.ScreenChat
	deps.Publish = r.publish
	r.state = ResidentState{ID: id, CollectiveID: resume, State: "idle", Updated: time.Now().UTC()}
	if journal != nil {
		if err := journal.PutRecord("resident", id, r.state); err != nil {
			cancel()
			deps.Registry.Close()
			return nil, err
		}
	}
	r.program = tea.NewProgram(NewRootModel(deps), tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	go func() {
		defer close(r.done)
		defer deps.Registry.Close()
		defer cancel()
		model, _ := r.program.Run()
		if root, ok := model.(RootModel); ok {
			root.chat = root.chat.Stop()
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		r.state.State = "stopped"
		r.state.Updated = time.Now().UTC()
		if journal != nil {
			_ = journal.PutRecord("resident", id, r.state)
		}
		r.frame.Closed = true
		for ch := range r.subscribers {
			select {
			case ch <- r.frame:
			default:
			}
			close(ch)
		}
		r.subscribers = map[chan RunnerFrame]bool{}
	}()
	return r, nil
}

func (r *SessionRunner) publish(frame RunnerFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := "idle"
	if frame.Busy {
		state = "running"
	}
	if frame.ApprovalID != "" {
		state = "waiting_approval"
	}
	if state != r.state.State || frame.CollectiveID != r.state.CollectiveID {
		next := ResidentState{r.ID, frame.CollectiveID, state, time.Now().UTC()}
		if r.journal != nil {
			if err := r.journal.PutRecord("resident", r.ID, next); err != nil {
				return err
			}
		}
		r.state = next
	}
	frame.Sequence = r.frame.Sequence + 1
	r.frame = frame
	for ch := range r.subscribers {
		select {
		case ch <- frame:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- frame:
			default:
			}
		}
	}
	return nil
}
func (r *SessionRunner) Subscribe() (<-chan RunnerFrame, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan RunnerFrame, 1)
	ch <- r.frame
	if r.frame.Closed {
		close(ch)
	} else {
		r.subscribers[ch] = true
	}
	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.subscribers[ch] {
			delete(r.subscribers, ch)
			close(ch)
		}
	}
}
func (r *SessionRunner) Input(input RunnerInput) error {
	// The caller may reuse its input buffers as soon as Send accepts the event.
	if input.Key != nil {
		key := *input.Key
		input.Key = &key
	}
	if input.Paste != nil {
		paste := *input.Paste
		input.Paste = &paste
	}
	r.mu.Lock()
	frame := r.frame
	r.mu.Unlock()
	if frame.Closed {
		return fmt.Errorf("session stopped")
	}
	if input.Key != nil {
		if frame.ApprovalID != "" && input.ApprovalID != frame.ApprovalID {
			return fmt.Errorf("approval changed; review the current request")
		}
		r.program.Send(runnerInputMsg{input})
	}
	if input.Paste != nil {
		if len(*input.Paste) > 32768 {
			return fmt.Errorf("paste exceeds 32 KiB")
		}
		r.program.Send(runnerInputMsg{input})
	}
	if input.Width > 0 && input.Height > 0 && input.Width <= 1000 && input.Height <= 500 {
		r.program.Send(tea.WindowSizeMsg{Width: input.Width, Height: input.Height})
	}
	return nil
}
func (r *SessionRunner) Status() (ResidentState, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state, len(r.subscribers)
}
func (r *SessionRunner) Stop()                 { r.cancel() }
func (r *SessionRunner) Done() <-chan struct{} { return r.done }
