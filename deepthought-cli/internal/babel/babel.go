// Package babel is DeepThought's backend abstraction (per docs/ARCHITECTURE.md): it owns
// the wire format and transport for talking to an inference gateway. Today that's a
// single OpenAI-compatible surface — the Vulcan KServe gateway — so this is a plain
// OpenAI chat-completions client. When an Anthropic-format backend lands, the
// per-family adapters live here and Unimatrix calls through a shared interface.
package babel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// MaxBody is a safety cap on response bodies we'll read off the wire. A chat reply
// is kilobytes; a misrouted gateway that returns HTML or a log dump shouldn't OOM us.
const MaxBody = 1 << 20 // 1 MiB

// Message is one chat message. Content is always emitted (no omitempty): the
// OpenAI wire wants a content field on system/user/assistant turns, and KServe/vLLM
// accepts "" for an assistant turn that carries only tool_calls. ToolCalls is set on
// assistant turns that request tools; ToolCallID is set on role:"tool" result turns.
type Message struct {
	Role       string     `json:"role"`                   // "system" | "user" | "assistant" | "tool"
	Content    string     `json:"content"`                // always emitted
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant turns only
	ToolCallID string     `json:"tool_call_id,omitempty"` // role:"tool" only
}

// ToolCall is one function call emitted by the model. On the request side it appears
// in an assistant message; on the stream side it is reassembled from delta fragments.
type ToolCall struct {
	ID       string       `json:"id"` // server-assigned id, e.g. "call_..."
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall carries the tool name and its arguments as a JSON string (the model
// streams arguments token-by-token; we concat then parse once).
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef is one entry in the request `tools` array (OpenAI function-calling shape).
type ToolDef struct {
	Type     string `json:"type"` // always "function"
	Function ToolFn `json:"function"`
}

// ToolFn is the function payload of a ToolDef.
type ToolFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema (type/properties/required)
}

// NewToolDef builds a function ToolDef with the type pre-filled.
func NewToolDef(name, description string, params map[string]any) ToolDef {
	return ToolDef{Type: "function", Function: ToolFn{Name: name, Description: description, Parameters: params}}
}

// ChatRequest is the call Unimatrix asks Babel to make.
type ChatRequest struct {
	Model          string    // wire model id, e.g. "qwen35-122b"
	Messages       []Message // full conversation incl. a leading system message
	MaxTokens      int       // completion cap; 0 = omit (server default)
	Temperature    float64   // 0 = omit (server default)
	Tools          []ToolDef // advertised function tools; empty = omit
	Effort         Effort    // off/low/medium/high/max, translated per model family
	ReasoningStyle string    // gptoss/qwen/gemma4/deepseek/anthropic/none
	Thinking       bool      // deprecated compatibility: true maps to medium/qwen
}

// Effort is the provider-neutral reasoning control.
type Effort string

const (
	EffortOff    Effort = "off"
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortMax    Effort = "max"
)

// Reply is one completed assistant turn: the visible text, the reasoning trace
// (empty unless Thinking was requested and the model emits one), any tool
// calls requested, and the token usage the gateway reported (zero if it didn't).
type Reply struct {
	Text      string
	Reasoning string
	ToolCalls []ToolCall
	Usage     Usage
}

// Usage is the token accounting the gateway returns for one turn. Fields are
// zero when the gateway didn't report usage (e.g. a streaming server that
// ignores stream_options.include_usage).
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// wireUsage mirrors the OpenAI "usage" object.
type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u *wireUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
}

// Client talks to one OpenAI-compatible gateway. Safe for concurrent use — the
// fields are immutable after NewClient and http.Client is goroutine-safe — so one
// client is shared across all SSH sessions.
type Client struct {
	BaseURL string // gateway URL incl. version prefix, e.g. .../serving/api/v1
	APIKey  string
	Wire    string
	HTTP    *http.Client
}

// NewClient builds a Client with a timeout generous enough for KServe cold starts
// (models scale to zero; first hit can warm for minutes before the first byte).
func NewClient(baseURL, apiKey string) *Client {
	return NewClientWithOptions(baseURL, apiKey, "openai", 6*time.Minute)
}

// NewClientWithOptions builds a client for one wire family and timeout.
func NewClientWithOptions(baseURL, apiKey, wire string, timeout time.Duration) *Client {
	if wire == "" {
		wire = "openai"
	}
	if timeout <= 0 {
		timeout = 6 * time.Minute
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Wire:    wire,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// wireRequest mirrors the OpenAI chat-completions request body. omitempty so a zero
// MaxTokens/Temperature is left to the server default rather than sent as 0.
type wireRequest struct {
	Model               string         `json:"model"`
	Messages            []Message      `json:"messages"`
	MaxTokens           int            `json:"max_tokens,omitempty"`
	Temperature         float64        `json:"temperature,omitempty"`
	Stream              bool           `json:"stream"`
	Tools               []ToolDef      `json:"tools,omitempty"`
	ChatTemplateKwargs  map[string]any `json:"chat_template_kwargs,omitempty"`
	ReasoningEffort     string         `json:"reasoning_effort,omitempty"`
	ThinkingTokenBudget *int           `json:"thinking_token_budget,omitempty"`
	StreamOptions       *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

// wireBody builds the request body for a call. Thinking maps to vLLM's
// chat_template_kwargs.enable_thinking — the knob the KServe gateway's Qwen
// models honor (verified live 2026-07-19: with it, thinking streams in a
// separate "reasoning" field; without it the model answers directly).
func wireBody(req ChatRequest, stream bool) wireRequest {
	w := wireRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      stream,
		Tools:       req.Tools,
	}
	if stream {
		// Ask the gateway to include token usage on the final SSE chunk. Servers
		// that ignore it just leave Reply.Usage zero; the dashboard handles that.
		w.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	applyEffort(&w, req)
	return w
}

func applyEffort(w *wireRequest, req ChatRequest) {
	effort := req.Effort
	style := strings.ToLower(req.ReasoningStyle)
	if effort == "" && req.Thinking {
		effort, style = EffortMedium, "qwen"
	}
	if effort == "" {
		return
	}
	kw := func(value bool) {
		if w.ChatTemplateKwargs == nil {
			w.ChatTemplateKwargs = map[string]any{}
		}
		w.ChatTemplateKwargs["enable_thinking"] = value
	}
	grade := func() string {
		switch effort {
		case EffortOff, EffortLow:
			return "low"
		case EffortMedium:
			return "medium"
		default:
			return "high"
		}
	}
	switch style {
	case "gptoss":
		w.ReasoningEffort = grade()
	case "qwen":
		if effort == EffortOff {
			kw(false)
			return
		}
		kw(true)
		budgets := map[Effort]int{EffortLow: 1024, EffortMedium: 4096, EffortHigh: 16384}
		if n, ok := budgets[effort]; ok {
			w.ThinkingTokenBudget = &n
		}
	case "gemma4":
		if effort != EffortOff {
			kw(true)
			w.ReasoningEffort = grade()
		}
	case "deepseek":
		if effort != EffortOff && effort != EffortMax {
			budgets := map[Effort]int{EffortLow: 1024, EffortMedium: 4096, EffortHigh: 16384}
			n := budgets[effort]
			w.ThinkingTokenBudget = &n
		}
	}
}

// wireResponse mirrors the OpenAI chat-completions response. Reasoning models
// return the thinking trace in a separate field — "reasoning" on the Vulcan
// gateway, "reasoning_content" on some other vLLM builds; we accept both.
type wireResponse struct {
	Choices []struct {
		Message struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Chat sends req to {BaseURL}/chat/completions and returns the assistant's reply.
// A non-2xx response is returned as an error that includes the status and a snippet
// of the body so gateway errors (cold-start 503s, bad model ids) are debuggable.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (Reply, error) {
	if c.Wire == "anthropic" {
		return c.chatAnthropic(ctx, req)
	}
	if c.APIKey == "" {
		return Reply{}, errors.New("babel: empty API key (set provider.api_key in config)")
	}
	if c.BaseURL == "" {
		return Reply{}, errors.New("babel: empty base URL")
	}

	reply, status, raw, err := c.chatOpenAIOnce(ctx, req)
	if err == nil {
		return reply, nil
	}
	if status == http.StatusBadRequest && req.Effort != "" {
		fallback := req
		fallback.Effort, fallback.ReasoningStyle, fallback.Thinking = "", "", false
		if retried, _, _, retryErr := c.chatOpenAIOnce(ctx, fallback); retryErr == nil {
			return retried, nil
		}
	}
	if status != 0 && status != http.StatusOK {
		return Reply{}, fmt.Errorf("babel: %d: %s", status, snippet(raw))
	}
	return Reply{}, err
}

func (c *Client) chatOpenAIOnce(ctx context.Context, req ChatRequest) (Reply, int, []byte, error) {
	buf, err := json.Marshal(wireBody(req, false))
	if err != nil {
		return Reply{}, 0, nil, fmt.Errorf("babel: marshal request: %w", err)
	}

	url := c.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return Reply{}, 0, nil, fmt.Errorf("babel: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Reply{}, 0, nil, fmt.Errorf("babel: request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return Reply{}, resp.StatusCode, raw, fmt.Errorf("babel: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return Reply{}, resp.StatusCode, raw, fmt.Errorf("babel: %s", resp.Status)
	}

	var wr wireResponse
	if err := json.Unmarshal(raw, &wr); err != nil {
		return Reply{}, resp.StatusCode, raw, fmt.Errorf("babel: parse response: %w", err)
	}
	if wr.Error != nil {
		return Reply{}, resp.StatusCode, raw, fmt.Errorf("babel: gateway error: %s", wr.Error.Message)
	}
	if len(wr.Choices) == 0 {
		return Reply{}, resp.StatusCode, raw, errors.New("babel: response had no choices")
	}
	msg := wr.Choices[0].Message
	text, inlineReasoning := extractThinkTags(msg.Content)
	return Reply{Text: text, Reasoning: coalesce(msg.Reasoning, msg.ReasoningContent, inlineReasoning), Usage: wr.Usage.usage()}, resp.StatusCode, raw, nil
}

// coalesce returns the first non-empty string.
func coalesce(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// snippet trims a body to a debug-friendly length for error messages.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// modelsResponse mirrors the OpenAI /models list endpoint.
type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ListModels fetches the IDs the gateway advertises at GET {BaseURL}/models.
// Used by the settings editor's "list models" discovery. Providers that don't
// implement the endpoint return an error (surfaced in the UI, not a crash).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if c.APIKey == "" {
		return nil, errors.New("babel: empty API key")
	}
	if c.BaseURL == "" {
		return nil, errors.New("babel: empty base URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("babel: build models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("babel: models request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return nil, fmt.Errorf("babel: read models: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("babel: models: %s: %s", resp.Status, snippet(raw))
	}
	var mr modelsResponse
	if err := json.Unmarshal(raw, &mr); err != nil {
		return nil, fmt.Errorf("babel: parse models: %w", err)
	}
	if mr.Error != nil {
		return nil, fmt.Errorf("babel: models gateway error: %s", mr.Error.Message)
	}
	ids := make([]string, 0, len(mr.Data))
	for _, m := range mr.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// StreamDelta is one chunk off the wire: visible content or reasoning
// (thinking) text. Exactly one field is non-empty per delta in practice.
type StreamDelta struct {
	Content   string
	Reasoning string
}

// StreamFn receives each delta as it arrives off the wire. Called from the
// goroutine reading the SSE stream; it must not block (the TUI side copies the
// delta onto a channel and returns).
type StreamFn func(d StreamDelta)

// streamChunk mirrors one OpenAI SSE data payload. Delta.Content is the streamed
// text; Delta.ToolCalls is the streamed function-call fragments (keyed by Index, with
// arguments arriving as partial-JSON string deltas that the caller concatenates).
// Reasoning models stream the thinking trace in Delta.Reasoning (or
// Delta.ReasoningContent on some vLLM builds) before any content arrives.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage,omitempty"`
}

// ChatStream is the streaming form of Chat: it POSTs with stream:true, parses the SSE
// event stream, calls fn for every delta as it arrives (content AND reasoning, as
// separate StreamDelta kinds), and returns the fully accumulated Reply including any
// tool calls the model requested (reassembled from their streamed fragments). A
// non-2xx status is an error (fn is not called). Errors mid-stream return the Reply
// accumulated so far plus the error, so a caller that already showed deltas can
// leave them on screen.
//
// Tool-call assembly: the first fragment for an index carries {id,type,name}; every
// fragment (including the first) may carry an arguments string delta. We accumulate
// per index, then return the calls sorted by index. arguments is left as a raw JSON
// string for the caller to parse against the tool's schema.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, fn StreamFn) (Reply, error) {
	if c.Wire == "anthropic" {
		return c.chatStreamAnthropic(ctx, req, fn)
	}
	if c.APIKey == "" {
		return Reply{}, errors.New("babel: empty API key (set provider.api_key in config)")
	}
	if c.BaseURL == "" {
		return Reply{}, errors.New("babel: empty base URL")
	}

	buf, err := json.Marshal(wireBody(req, true))
	if err != nil {
		return Reply{}, fmt.Errorf("babel: marshal request: %w", err)
	}

	url := c.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return Reply{}, fmt.Errorf("babel: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Reply{}, fmt.Errorf("babel: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
		if resp.StatusCode == http.StatusBadRequest && req.Effort != "" {
			fallback := req
			fallback.Effort, fallback.ReasoningStyle, fallback.Thinking = "", "", false
			return c.ChatStream(ctx, fallback, fn)
		}
		return Reply{}, fmt.Errorf("babel: %s: %s", resp.Status, snippet(raw))
	}

	var acc, racc strings.Builder
	byIndex := map[int]*accCall{}
	var usage Usage // last non-zero usage seen (OpenAI sends it on the final chunk)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20) // 1 MiB max line (huge deltas / tool payloads)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, ":") { // blank separator or SSE comment
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue // ignore event:/id:/retry: etc.
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var ch streamChunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			continue // skip malformed/keep-alive lines rather than aborting a good stream
		}
		if len(ch.Choices) == 0 {
			// A chunk with usage but no choices (the terminal usage frame some
			// gateways send) still carries accounting — capture it.
			if ch.Usage != nil {
				usage = ch.Usage.usage()
			}
			continue
		}
		d := ch.Choices[0].Delta
		if reasoning := coalesce(d.Reasoning, d.ReasoningContent); reasoning != "" {
			racc.WriteString(reasoning)
			fn(StreamDelta{Reasoning: reasoning})
		}
		if d.Content != "" {
			acc.WriteString(d.Content)
			fn(StreamDelta{Content: d.Content})
		}
		if ch.Usage != nil {
			usage = ch.Usage.usage()
		}
		for _, tc := range d.ToolCalls {
			slot, ok := byIndex[tc.Index]
			if !ok {
				slot = &accCall{}
				byIndex[tc.Index] = slot
			}
			if tc.ID != "" {
				slot.call.ID = tc.ID
			}
			if tc.Type != "" {
				slot.call.Type = tc.Type
			}
			if tc.Function.Name != "" {
				slot.call.Function.Name = tc.Function.Name
			}
			slot.call.Function.Arguments += tc.Function.Arguments
		}
	}
	text, inlineReasoning := extractThinkTags(acc.String())
	reply := Reply{Text: text, Reasoning: coalesce(racc.String(), inlineReasoning), ToolCalls: assembledToolCalls(byIndex), Usage: usage}
	if err := sc.Err(); err != nil {
		return reply, fmt.Errorf("babel: read stream: %w", err)
	}
	return reply, nil
}

func extractThinkTags(content string) (text, reasoning string) {
	start := strings.Index(content, "<think>")
	if start < 0 {
		return content, ""
	}
	end := strings.Index(content[start+len("<think>"):], "</think>")
	if end < 0 {
		return content, ""
	}
	end += start + len("<think>")
	reasoning = strings.TrimSpace(content[start+len("<think>") : end])
	text = strings.TrimSpace(content[:start] + content[end+len("</think>"):])
	return text, reasoning
}

// accCall is the per-index accumulator slot used while reassembling streamed
// tool-call fragments. Pointer so deltas mutate in place.
type accCall struct{ call ToolCall }

// assembledToolCalls flattens the index-keyed accumulator into an index-sorted slice.
func assembledToolCalls(m map[int]*accCall) []ToolCall {
	if len(m) == 0 {
		return nil
	}
	idxs := make([]int, 0, len(m))
	for i := range m {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	out := make([]ToolCall, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, m[i].call)
	}
	return out
}
