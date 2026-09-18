package babel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestChatHappyPath asserts the request is shaped and authorized correctly and the
// first choice's content is returned.
func TestChatHappyPath(t *testing.T) {
	var gotAuth, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello there"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok-123")
	rep, err := c.Chat(context.Background(), ChatRequest{
		Model:    "qwen35-122b",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if rep.Text != "hello there" {
		t.Errorf("Chat = %q, want %q", rep.Text, "hello there")
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if !strings.Contains(gotBody, `"model":"qwen35-122b"`) {
		t.Errorf("body missing model: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"stream":false`) {
		t.Errorf("body missing stream:false: %s", gotBody)
	}
	if strings.Contains(gotBody, "max_tokens") {
		t.Errorf("zero MaxTokens should be omitted, body: %s", gotBody)
	}
}

func TestChatErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model warming up", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	_, err := c.Chat(context.Background(), ChatRequest{Model: "x", Messages: []Message{{Role: "user", Content: "y"}}})
	if err == nil {
		t.Fatal("Chat: want error for 503, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention status 503: %v", err)
	}
	if !strings.Contains(err.Error(), "warming up") {
		t.Errorf("error should include body snippet: %v", err)
	}
}

func TestChatGatewayErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"message":"bad model id","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	_, err := c.Chat(context.Background(), ChatRequest{Model: "x", Messages: []Message{{Role: "user", Content: "y"}}})
	if err == nil || !strings.Contains(err.Error(), "bad model id") {
		t.Fatalf("want gateway error, got: %v", err)
	}
}

func TestChatEmptyKey(t *testing.T) {
	c := NewClient("https://example", "")
	_, err := c.Chat(context.Background(), ChatRequest{Model: "x"})
	if err == nil || !strings.Contains(err.Error(), "empty API key") {
		t.Fatalf("want empty-key error, got: %v", err)
	}
}

func TestChatNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	_, err := c.Chat(context.Background(), ChatRequest{Model: "x", Messages: []Message{{Role: "user", Content: "y"}}})
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("want no-choices error, got: %v", err)
	}
}

// TestChatStream verifies SSE parsing: the request sets stream:true, and each
// choices[0].delta.content is delivered to the callback in order, with the full
// text accumulated and returned at the end.
func TestChatStream(t *testing.T) {
	var gotStreamFlag bool
	var gotCT string
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":""}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"lo"}}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Accept")
		b, _ := io.ReadAll(r.Body)
		gotStreamFlag = strings.Contains(string(b), `"stream":true`)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	var deltas []string
	c := NewClient(srv.URL, "tok")
	rep, err := c.ChatStream(context.Background(), ChatRequest{
		Model:     "x",
		Messages:  []Message{{Role: "user", Content: "y"}},
		MaxTokens: 10,
	}, func(d StreamDelta) { deltas = append(deltas, d.Content) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if gotCT != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream", gotCT)
	}
	if !gotStreamFlag {
		t.Error("request body did not contain stream:true")
	}
	if rep.Text != "Hello" {
		t.Errorf("accumulated = %q, want Hello", rep.Text)
	}
	if strings.Join(deltas, "") != "Hello" {
		t.Errorf("deltas = %v, want [Hel lo]", deltas)
	}
	if len(rep.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(rep.ToolCalls))
	}
}

func TestChatStreamErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "warming", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok")
	_, err := c.ChatStream(context.Background(), ChatRequest{Model: "x", Messages: []Message{{Role: "user", Content: "y"}}}, func(StreamDelta) {})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want 503 error, got: %v", err)
	}
}

// TestChatStreamToolCallAssembly feeds fragmented tool_call deltas (one seeding
// {id,type,name}, several carrying arguments fragments) and asserts they reassemble
// into a single ToolCall with the full JSON arguments — matching the shape the real
// KServe gateway emits.
func TestChatStreamToolCallAssembly(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"bash","arguments":""}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{"}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"command\": \"ls\""}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	rep, err := c.ChatStream(context.Background(), ChatRequest{Model: "x", Messages: []Message{{Role: "user", Content: "y"}}}, func(StreamDelta) {})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if len(rep.ToolCalls) != 1 {
		t.Fatalf("got %d calls, want 1: %+v", len(rep.ToolCalls), rep.ToolCalls)
	}
	got := rep.ToolCalls[0]
	if got.ID != "call_abc" || got.Type != "function" || got.Function.Name != "bash" {
		t.Errorf("assembled call = %+v", got)
	}
	if want := `{"command": "ls"}`; got.Function.Arguments != want {
		t.Errorf("arguments = %q, want %q", got.Function.Arguments, want)
	}
}

// TestChatThinkingRequest asserts Thinking maps to the vLLM
// chat_template_kwargs.enable_thinking knob and that a reasoning field in the
// response lands on the Reply (both "reasoning" and "reasoning_content" spellings).
func TestChatThinkingRequest(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"42","reasoning_content":"17+25 is 42"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	rep, err := c.Chat(context.Background(), ChatRequest{
		Model:    "qwen35-122b",
		Messages: []Message{{Role: "user", Content: "17+25?"}},
		Thinking: true,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(gotBody, `"chat_template_kwargs":{"enable_thinking":true}`) {
		t.Errorf("body missing enable_thinking kwarg: %s", gotBody)
	}
	if rep.Reasoning != "17+25 is 42" {
		t.Errorf("Reasoning = %q, want the trace", rep.Reasoning)
	}
	if rep.Text != "42" {
		t.Errorf("Text = %q, want 42", rep.Text)
	}
}

// TestChatStreamReasoning asserts reasoning deltas stream as their own
// StreamDelta kind, accumulate onto the Reply, and never leak into content.
func TestChatStreamReasoning(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":""}}]}`,
		``,
		`data: {"choices":[{"delta":{"reasoning":"Thinking"}}]}`,
		``,
		`data: {"choices":[{"delta":{"reasoning":" Process"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"42"}}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	var reasoning, content []string
	c := NewClient(srv.URL, "tok")
	rep, err := c.ChatStream(context.Background(), ChatRequest{
		Model:    "x",
		Messages: []Message{{Role: "user", Content: "y"}},
		Thinking: true,
	}, func(d StreamDelta) {
		reasoning = append(reasoning, d.Reasoning)
		content = append(content, d.Content)
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if rep.Reasoning != "Thinking Process" {
		t.Errorf("Reasoning = %q, want %q", rep.Reasoning, "Thinking Process")
	}
	if rep.Text != "42" {
		t.Errorf("Text = %q, want 42", rep.Text)
	}
	if strings.Join(reasoning, "") != "Thinking Process" {
		t.Errorf("reasoning deltas = %v", reasoning)
	}
	if strings.Join(content, "") != "42" {
		t.Errorf("content deltas = %v, want [42]", content)
	}
}

func TestEffortTranslationByModelFamily(t *testing.T) {
	cases := []struct {
		name  string
		req   ChatRequest
		wants []string
	}{
		{
			name:  "gptoss",
			req:   ChatRequest{Effort: EffortMax, ReasoningStyle: "gptoss"},
			wants: []string{`"reasoning_effort":"high"`},
		},
		{
			name:  "qwen",
			req:   ChatRequest{Effort: EffortMedium, ReasoningStyle: "qwen"},
			wants: []string{`"enable_thinking":true`, `"thinking_token_budget":4096`},
		},
		{
			name:  "qwen off",
			req:   ChatRequest{Effort: EffortOff, ReasoningStyle: "qwen"},
			wants: []string{`"enable_thinking":false`},
		},
		{
			name:  "gemma",
			req:   ChatRequest{Effort: EffortHigh, ReasoningStyle: "gemma4"},
			wants: []string{`"enable_thinking":true`, `"reasoning_effort":"high"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(wireBody(tc.req, false))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(string(raw), want) {
					t.Errorf("body %s missing %s", raw, want)
				}
			}
		})
	}
}

func TestThinkTagFallback(t *testing.T) {
	text, reasoning := extractThinkTags("<think>private trace</think>\nvisible")
	if text != "visible" || reasoning != "private trace" {
		t.Fatalf("text=%q reasoning=%q", text, reasoning)
	}
}

func TestAnthropicChatAndToolShape(t *testing.T) {
	var gotPath, gotVersion, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotVersion = r.Header.Get("anthropic-version")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"content":[
			{"type":"thinking","thinking":"considering"},
			{"type":"text","text":"done"},
			{"type":"tool_use","id":"tool_1","name":"bash","input":{"command":"pwd"}}
		]}`))
	}))
	defer srv.Close()

	c := NewClientWithOptions(srv.URL, "tok", "anthropic", time.Minute)
	reply, err := c.Chat(context.Background(), ChatRequest{
		Model: "claude-test",
		Messages: []Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "go"},
		},
		Tools:     []ToolDef{NewToolDef("bash", "run", map[string]any{"type": "object"})},
		MaxTokens: 2048,
		Effort:    EffortHigh, ReasoningStyle: "anthropic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/messages" || gotVersion == "" {
		t.Fatalf("path=%q version=%q", gotPath, gotVersion)
	}
	for _, want := range []string{`"system":"system"`, `"input_schema"`, `"type":"adaptive"`, `"effort":"high"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %s: %s", want, gotBody)
		}
	}
	if reply.Text != "done" || reply.Reasoning != "considering" || len(reply.ToolCalls) != 1 {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestRawJSONInvoke(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/predict" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"score":0.9}`))
	}))
	defer srv.Close()
	client := NewClientWithOptions(srv.URL, "token", "json", time.Minute)
	var output struct {
		Score float64 `json:"score"`
	}
	if err := client.InvokeJSON(context.Background(), "predict", map[string]int{"x": 1}, &output); err != nil {
		t.Fatal(err)
	}
	if output.Score != 0.9 {
		t.Fatalf("output=%+v", output)
	}
}
