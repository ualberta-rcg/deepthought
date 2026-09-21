package app

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/queen"
	"deepthought-cli/internal/tools"
	"deepthought-cli/internal/unimatrix"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type runnerFixtureTool struct{ calls *atomic.Int32 }

func (t runnerFixtureTool) Name() string        { return "fixture" }
func (t runnerFixtureTool) Description() string { return "fixture side effect" }
func (t runnerFixtureTool) ReadOnly() bool      { return false }
func (t runnerFixtureTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t runnerFixtureTool) Run(context.Context, map[string]any) tools.Result {
	t.calls.Add(1)
	return tools.Result{Content: "done", Summary: "fixture completed"}
}

func TestResidentDetachApprovalAndExactlyOnceExecution(t *testing.T) {
	var requests, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"fixture_call\",\"type\":\"function\",\"function\":{\"name\":\"fixture\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Complete\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	root := t.TempDir()
	store, err := history.NewSQLiteStore(filepath.Join(root, "history.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg, err := config.Validate(config.File{Providers: []config.Provider{{Name: "fixture", BaseURL: server.URL, Anonymous: true}}, Models: []unimatrix.Model{{ID: "fixture", Provider: "fixture", Capabilities: []unimatrix.Capability{unimatrix.CapChat, unimatrix.CapTools}, Context: 65536}}, Roles: map[string]string{unimatrix.RoleAgentic: "fixture", unimatrix.RoleSummary: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	deps := Deps{Live: NewSettings(cfg, filepath.Join(root, "config.json")), Registry: tools.NewRegistry(runnerFixtureTool{&calls}), Gate: queen.NewGate(queen.Review), ChatSource: func() history.ChatStore { return store }, DisableMonitoring: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner, err := NewSessionRunner(ctx, "fixture", deps, store, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { runner.Stop(); <-runner.Done() }()
	frames, detach := runner.Subscribe()
	if err := runner.Input(RunnerInput{Width: 80, Height: 24}); err != nil {
		t.Fatal(err)
	}
	key := tea.KeyPressMsg{Code: 'r', Text: "run fixture"}
	runner.Input(RunnerInput{Key: &key})
	key = tea.KeyPressMsg{Code: tea.KeyEnter}
	runner.Input(RunnerInput{Key: &key})
	wait := func(ch <-chan RunnerFrame, predicate func(RunnerFrame) bool) RunnerFrame {
		t.Helper()
		for {
			select {
			case f, ok := <-ch:
				if !ok {
					t.Fatal("session ended")
				}
				if predicate(f) {
					return f
				}
			case <-ctx.Done():
				t.Fatal("timed out waiting for resident state")
			}
		}
	}
	pending := wait(frames, func(f RunnerFrame) bool { return f.ApprovalID != "" })
	detach()
	state, attached := runner.Status()
	if state.State != "waiting_approval" || attached != 0 || calls.Load() != 0 {
		t.Fatalf("detached state %+v, attached=%d calls=%d", state, attached, calls.Load())
	}
	frames, detach = runner.Subscribe()
	defer detach()
	key = tea.KeyPressMsg{Code: 'y', Text: "y"}
	if err := runner.Input(RunnerInput{Key: &key, ApprovalID: "stale"}); err == nil {
		t.Fatal("stale approval accepted")
	}
	if err := runner.Input(RunnerInput{Key: &key, ApprovalID: pending.ApprovalID}); err != nil {
		t.Fatal(err)
	}
	finished := wait(frames, func(f RunnerFrame) bool { return !f.Busy && f.ApprovalID == "" && calls.Load() == 1 })
	if calls.Load() != 1 {
		t.Fatal("tool executed more than once")
	}
	if _, err := store.Resume(finished.CollectiveID); err != nil {
		t.Fatal("resident history not persisted:", err)
	}
}
