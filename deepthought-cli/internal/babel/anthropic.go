package babel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type anthropicRequest struct {
	Model        string             `json:"model"`
	System       string             `json:"system,omitempty"`
	Messages     []anthropicMessage `json:"messages"`
	MaxTokens    int                `json:"max_tokens"`
	Temperature  *float64           `json:"temperature,omitempty"`
	Stream       bool               `json:"stream"`
	Tools        []anthropicTool    `json:"tools,omitempty"`
	Thinking     map[string]any     `json:"thinking,omitempty"`
	OutputConfig map[string]any     `json:"output_config,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicResponse struct {
	Content []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	} `json:"content"`
	Usage *anthropicUsage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// anthropicUsage mirrors the Anthropic "usage" object. Note the JSON keys are
// input_tokens/output_tokens (NOT OpenAI's prompt_tokens/completion_tokens), so
// this is a distinct type from wireUsage. Returned by both the non-stream
// response and the message_start/message_delta SSE events.
type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (u *anthropicUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens, TotalTokens: u.InputTokens + u.OutputTokens}
}

func (c *Client) anthropicURL() string {
	if strings.HasSuffix(c.BaseURL, "/v1") {
		return c.BaseURL + "/messages"
	}
	return c.BaseURL + "/v1/messages"
}

func buildAnthropicRequest(req ChatRequest, stream, includeEffort bool) anthropicRequest {
	out := anthropicRequest{Model: req.Model, Stream: stream, MaxTokens: req.MaxTokens}
	if out.MaxTokens <= 0 {
		out.MaxTokens = 4096
	}
	if req.Temperature != 0 {
		t := req.Temperature
		out.Temperature = &t
	}
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, anthropicTool{
			Name: tool.Function.Name, Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			if out.System != "" {
				out.System += "\n\n"
			}
			out.System += msg.Content
		case "assistant":
			var blocks []map[string]any
			if msg.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": msg.Content})
			}
			for _, call := range msg.ToolCalls {
				var input any = map[string]any{}
				if call.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(call.Function.Arguments), &input)
				}
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": input,
				})
			}
			out.Messages = append(out.Messages, anthropicMessage{Role: "assistant", Content: blocks})
		case "tool":
			block := map[string]any{
				"type": "tool_result", "tool_use_id": msg.ToolCallID, "content": msg.Content,
			}
			out.Messages = append(out.Messages, anthropicMessage{Role: "user", Content: []map[string]any{block}})
		default:
			out.Messages = append(out.Messages, anthropicMessage{Role: "user", Content: msg.Content})
		}
	}
	if includeEffort && req.Effort != "" && req.Effort != EffortOff {
		if strings.EqualFold(req.ReasoningStyle, "anthropic") {
			out.Thinking = map[string]any{"type": "adaptive"}
			out.OutputConfig = map[string]any{"effort": anthropicEffort(req.Effort)}
		} else {
			out.Thinking = map[string]any{"type": "enabled", "budget_tokens": anthropicBudget(req.Effort)}
		}
	}
	return out
}

func anthropicEffort(e Effort) string {
	switch e {
	case EffortLow, EffortMedium, EffortHigh, EffortMax:
		return string(e)
	default:
		return "medium"
	}
}

func anthropicBudget(e Effort) int {
	switch e {
	case EffortLow:
		return 1024
	case EffortHigh:
		return 16384
	case EffortMax:
		return 32768
	default:
		return 4096
	}
}

func (c *Client) newAnthropicRequest(ctx context.Context, payload any) (*http.Request, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("babel: marshal anthropic request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.anthropicURL(), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("babel: build anthropic request: %w", err)
	}
	req.Header.Set("x-api-key", c.APIKey)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) chatAnthropic(ctx context.Context, req ChatRequest) (Reply, error) {
	if c.APIKey == "" && !c.AllowAnonymous {
		return Reply{}, fmt.Errorf("babel: empty API key (set provider.api_key in config)")
	}
	reply, status, raw, err := c.chatAnthropicOnce(ctx, req, true)
	if err == nil {
		return reply, nil
	}
	if status == http.StatusBadRequest && req.Effort != "" && effortRejected(raw) {
		returned, _, _, retryErr := c.chatAnthropicOnce(ctx, req, false)
		if retryErr == nil {
			return returned, nil
		}
	}
	return Reply{}, fmt.Errorf("babel: anthropic: %w: %s", err, snippet(raw))
}

func (c *Client) chatAnthropicOnce(ctx context.Context, req ChatRequest, effort bool) (Reply, int, []byte, error) {
	httpReq, err := c.newAnthropicRequest(ctx, buildAnthropicRequest(req, false, effort))
	if err != nil {
		return Reply{}, 0, nil, err
	}
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Reply{}, 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return Reply{}, resp.StatusCode, raw, err
	}
	if resp.StatusCode != http.StatusOK {
		return Reply{}, resp.StatusCode, raw, fmt.Errorf("%s", resp.Status)
	}
	var wire anthropicResponse
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Reply{}, resp.StatusCode, raw, err
	}
	if wire.Error != nil {
		return Reply{}, resp.StatusCode, raw, fmt.Errorf("%s", wire.Error.Message)
	}
	var text, reasoning strings.Builder
	var calls []ToolCall
	for _, block := range wire.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking":
			reasoning.WriteString(block.Thinking)
		case "tool_use":
			calls = append(calls, ToolCall{ID: block.ID, Type: "function", Function: FunctionCall{
				Name: block.Name, Arguments: string(block.Input),
			}})
		}
	}
	clean, inline := extractThinkTags(text.String())
	return Reply{Text: clean, Reasoning: coalesce(reasoning.String(), inline), ToolCalls: calls, Usage: wire.Usage.usage()}, resp.StatusCode, raw, nil
}

type anthropicEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	// message_start carries message.usage (input_tokens); message_delta carries
	// a top-level usage (output_tokens). Used for token accounting.
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage anthropicUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) chatStreamAnthropic(ctx context.Context, req ChatRequest, fn StreamFn) (Reply, error) {
	if c.APIKey == "" && !c.AllowAnonymous {
		return Reply{}, fmt.Errorf("babel: empty API key (set provider.api_key in config)")
	}
	httpReq, err := c.newAnthropicRequest(ctx, buildAnthropicRequest(req, true, true))
	if err != nil {
		return Reply{}, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Reply{}, fmt.Errorf("babel: anthropic stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
		if resp.StatusCode == http.StatusBadRequest && req.Effort != "" && effortRejected(raw) {
			fallback := req
			fallback.Effort = ""
			return c.chatStreamAnthropic(ctx, fallback, fn)
		}
		return Reply{}, fmt.Errorf("babel: anthropic %s: %s", resp.Status, snippet(raw))
	}

	var text, reasoning strings.Builder
	var usage Usage // accumulated from message_start (input) + message_delta (output)
	type pendingCall struct {
		call ToolCall
		args strings.Builder
	}
	calls := map[int]*pendingCall{}
	finished := false
	var streamErr error
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event anthropicEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			streamErr = fmt.Errorf("babel: malformed stream event: %w", err)
			break
		}
		if event.Error != nil {
			return Reply{Text: text.String(), Reasoning: reasoning.String()}, fmt.Errorf("babel: anthropic: %s", event.Error.Message)
		}
		switch event.Type {
		case "message_stop":
			finished = true
		case "message_start":
			// input_tokens arrive here (prompt accounting).
			usage.PromptTokens = event.Message.Usage.InputTokens
		case "message_delta":
			// output_tokens arrive in the terminal message_delta's usage.
			if event.Usage.OutputTokens > 0 {
				usage.CompletionTokens = event.Usage.OutputTokens
			}
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				calls[event.Index] = &pendingCall{call: ToolCall{
					ID: event.ContentBlock.ID, Type: "function",
					Function: FunctionCall{Name: event.ContentBlock.Name, Arguments: string(event.ContentBlock.Input)},
				}}
			}
		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				text.WriteString(event.Delta.Text)
				fn(StreamDelta{Content: event.Delta.Text})
			case "thinking_delta":
				reasoning.WriteString(event.Delta.Thinking)
				fn(StreamDelta{Reasoning: event.Delta.Thinking})
			case "input_json_delta":
				if call := calls[event.Index]; call != nil {
					call.args.WriteString(event.Delta.PartialJSON)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		streamErr = fmt.Errorf("babel: anthropic stream: %w", err)
	}
	ordered := make(map[int]*accCall, len(calls))
	for idx, call := range calls {
		if call.args.Len() > 0 {
			call.call.Function.Arguments = call.args.String()
		}
		if call.call.Function.Arguments == "" {
			call.call.Function.Arguments = "{}"
		}
		ordered[idx] = &accCall{call: call.call}
	}
	clean, inline := extractThinkTags(text.String())
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	if streamErr == nil && !finished {
		streamErr = fmt.Errorf("babel: anthropic stream ended without message_stop")
	}
	return Reply{
		Text: clean, Reasoning: coalesce(reasoning.String(), inline),
		ToolCalls: assembledToolCalls(ordered), Usage: usage,
	}, streamErr
}
