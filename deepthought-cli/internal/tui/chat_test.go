package tui

import (
	"testing"

	"annorax/internal/babel"
	"annorax/internal/history"
	"annorax/internal/queen"
	"annorax/internal/tools"
	"annorax/internal/unimatrix"
)

// fakeSource is a test InferenceSource that never reaches the network.
type fakeSource struct{}

func (fakeSource) RoleClient(role string) (*babel.Client, unimatrix.Model, error) {
	return nil, unimatrix.Model{ID: "qwen35-122b"}, nil
}
func (fakeSource) ThinkingEnabled() bool       { return false }
func (fakeSource) Path() string                { return "test-config.json" }
func (fakeSource) CycleModel() (string, error) { return "test-model", nil }

// fileSource wraps a chat dir as a ChatStoreSource (a fresh FileStore per chat),
// matching how main.go builds the source for the live app.
func fileSource(dir string) history.ChatStoreSource {
	return func() history.ChatStore { return history.NewFileStore(dir) }
}

func TestRequestMessagesFromCollective(t *testing.T) {
	reg := tools.NewRegistry(tools.NewBash(), tools.NewRead())
	gate := queen.NewGate(queen.Review)
	m := NewChatModel(fakeSource{}, reg, gate, "test_session", fileSource(t.TempDir()))

	inc := m.coll.StartIncursion("hello")
	tx, _ := inc.AddTransmission("Hi!", nil)
	tx.Text = "Hi!"
	inc.MarkCompleted()

	msgs := m.requestMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (system, user, assistant), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Fatalf("msg[0].Role = %q, want system", msgs[0].Role)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hello" {
		t.Fatalf("msg[1] = %+v, want user/hello", msgs[1])
	}
	if msgs[2].Role != "assistant" || msgs[2].Content != "Hi!" {
		t.Fatalf("msg[2] = %+v, want assistant/Hi!", msgs[2])
	}
}

func TestIncursionCapOnCollective(t *testing.T) {
	reg := tools.NewRegistry(tools.NewBash(), tools.NewRead())
	gate := queen.NewGate(queen.Review)
	m := NewChatModel(fakeSource{}, reg, gate, "test_session", fileSource(t.TempDir()))
	m.coll.MaxCycles = 2

	inc := m.coll.StartIncursion("loop")
	inc.Status = history.IncursionDispatching
	m.dispatch = &dispatchState{
		transmission: &history.Transmission{
			Text:   "ok",
			Probes: []*history.Probe{},
		},
	}

	// Simulate hitting the cap after the maximum cycles.
	inc.Cycles = m.coll.MaxCycles - 1
	_, cmd := m.processCurrentTool()
	if cmd != nil {
		t.Fatal("expected no command after hitting cap")
	}
	if m.busy {
		t.Fatal("expected busy=false after cap")
	}
	if inc.Status != history.IncursionFailed {
		t.Fatalf("expected incursion failed, got %s", inc.Status)
	}
	if inc.Cycles != m.coll.MaxCycles {
		t.Fatalf("expected Cycles=%d, got %d", m.coll.MaxCycles, inc.Cycles)
	}
}

func TestMultiCycleIncursionSerialization(t *testing.T) {
	reg := tools.NewRegistry(tools.NewBash(), tools.NewRead())
	gate := queen.NewGate(queen.Review)
	m := NewChatModel(fakeSource{}, reg, gate, "test_session", fileSource(t.TempDir()))

	inc := m.coll.StartIncursion("do two things")
	inc.Status = history.IncursionDispatching

	// First assistant transmission + probe.
	tx1, _ := inc.AddTransmission("First, read a file.", []babel.ToolCall{
		{ID: "call_a", Type: "function", Function: babel.FunctionCall{Name: "read", Arguments: `{"file_path":"/tmp/a"}`}},
	})
	tx1.Probes[0].Result = history.ResultView{
		Content: "content A",
		Summaries: history.SummarySet{
			Full: "content A",
		},
	}

	// Second assistant transmission + probe within the same incursion.
	tx2, _ := inc.AddTransmission("Now run a command.", []babel.ToolCall{
		{ID: "call_b", Type: "function", Function: babel.FunctionCall{Name: "bash", Arguments: `{"command":"echo done"}`}},
	})
	tx2.Probes[0].Result = history.ResultView{
		Content: "done",
		Summaries: history.SummarySet{
			Full: "done",
		},
	}

	inc.MarkCompleted()

	msgs := m.requestMessages()
	// system, user, assistant(call_a), tool(call_a), assistant(call_b), tool(call_b) = 6
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[2].Role != "assistant" || len(msgs[2].ToolCalls) != 1 || msgs[2].ToolCalls[0].ID != "call_a" {
		t.Fatalf("msg[2] should be assistant with call_a, got %+v", msgs[2])
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "call_a" {
		t.Fatalf("msg[3] should be tool result for call_a, got %+v", msgs[3])
	}
	if msgs[4].Role != "assistant" || len(msgs[4].ToolCalls) != 1 || msgs[4].ToolCalls[0].ID != "call_b" {
		t.Fatalf("msg[4] should be assistant with call_b, got %+v", msgs[4])
	}
	if msgs[5].Role != "tool" || msgs[5].ToolCallID != "call_b" {
		t.Fatalf("msg[5] should be tool result for call_b, got %+v", msgs[5])
	}
}

func TestHandleToolResultMutatesProbe(t *testing.T) {
	reg := tools.NewRegistry(tools.NewBash(), tools.NewRead())
	gate := queen.NewGate(queen.Review)
	m := NewChatModel(fakeSource{}, reg, gate, "test_session", fileSource(t.TempDir()))

	inc := m.coll.StartIncursion("run")
	inc.Status = history.IncursionDispatching
	tx, _ := inc.AddTransmission("I'll run it.", []babel.ToolCall{
		{ID: "call_1", Type: "function", Function: babel.FunctionCall{Name: "bash", Arguments: `{"command":"echo hi"}`}},
	})
	m.dispatch = &dispatchState{transmission: tx}

	probe := tx.Probes[0]
	probe.Decision = queen.Allow
	probe.Status = history.ProbeRunning

	m.handleToolResult(toolResultMsg{
		probe: probe,
		result: tools.Result{
			Content: "hi",
			Summary: "bash → exit 0",
			IsError: false,
		},
	})

	if probe.Status != history.ProbeCompleted {
		t.Fatalf("expected probe completed, got %s", probe.Status)
	}
	if probe.Result.Content != "hi" {
		t.Fatalf("unexpected result content: %q", probe.Result.Content)
	}
	if probe.Result.Summaries.Full != "hi" {
		t.Fatalf("expected Full summary set, got %+v", probe.Result.Summaries)
	}
	if probe.Result.Summaries.Condensed != "" {
		t.Fatalf("chat loop should not auto-generate condensed summaries, got %q", probe.Result.Summaries.Condensed)
	}
}

func TestInterruptPreservesPartialTransmission(t *testing.T) {
	m := NewChatModel(
		fakeSource{},
		tools.NewRegistry(tools.NewBash(), tools.NewRead()),
		queen.NewGate(queen.Review),
		"test_session",
		fileSource(t.TempDir()),
	)
	inc := m.coll.StartIncursion("long task")
	inc.Status = history.IncursionStreaming
	m.busy = true
	m.streaming = true
	m.acc = "partial answer"
	m.thinkAcc = "partial reasoning"
	m.lines = []string{"", "pending"}
	m.pendIdx = 1

	got, _ := m.interrupt()
	if inc.Status != history.IncursionInterrupted {
		t.Fatalf("status=%s", inc.Status)
	}
	if len(inc.Transmissions) != 1 || inc.Transmissions[0].Text != "partial answer" {
		t.Fatalf("partial transmission not preserved: %+v", inc.Transmissions)
	}
	if len(inc.Transmissions[0].Synapses) != 1 {
		t.Fatal("partial reasoning not preserved")
	}
	if got.coll.LastInterrupted() != inc {
		t.Fatal("interrupted turn is not resumable")
	}
}
