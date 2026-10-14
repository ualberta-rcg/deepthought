package unimatrix

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/queen"
	"deepthought-cli/internal/tools"
)

type ClientResolver interface {
	RoleClient(string) (*babel.Client, Model, error)
	Effort() babel.Effort
}

type Approver func(context.Context, babel.ToolCall) bool
type EventSink func(history.Drone)

// Session is the headless agent/tool loop. TUI, daemon, schedule, and plan
// execution can all observe the same loop without owning it.
type Session struct {
	ID        string
	System    string
	Resolver  ClientResolver
	Registry  *tools.Registry
	Gate      *queen.Gate
	Approve   Approver
	OnEvent   EventSink
	MaxCycles int

	mu       sync.Mutex
	messages []babel.Message
}

func NewSession(id, system string, resolver ClientResolver, registry *tools.Registry, gate *queen.Gate) *Session {
	return &Session{
		ID: id, System: system, Resolver: resolver, Registry: registry, Gate: gate,
		MaxCycles: 10, messages: []babel.Message{{Role: "system", Content: system}},
	}
}

func (s *Session) Messages() []babel.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]babel.Message(nil), s.messages...)
}

// RunTurn executes one user turn through all requested tools until a plain
// response, rejection, interruption, or cycle cap.
func (s *Session) RunTurn(ctx context.Context, prompt string, stream babel.StreamFn) (babel.Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(prompt) == "" {
		return babel.Reply{}, fmt.Errorf("unimatrix: empty prompt")
	}
	s.messages = append(s.messages, babel.Message{Role: "user", Content: prompt})
	s.emit("hail", map[string]string{"text": prompt})
	for cycle := 0; cycle < s.MaxCycles; cycle++ {
		client, model, err := s.Resolver.RoleClient(RoleAgentic)
		if err != nil {
			return babel.Reply{}, err
		}
		// Effort is the sole reasoning control (off = no thinking). Default to
		// medium when unset; a non-reasoning model never thinks.
		effort := s.Resolver.Effort()
		if effort == "" {
			effort = babel.EffortMedium
		}
		if !model.Can(CapReasoning) {
			effort = babel.EffortOff
		}
		reply, err := client.ChatStream(ctx, babel.ChatRequest{
			Model: model.RequestID(), Messages: s.messages, Tools: s.Registry.Schemas(),
			MaxTokens: 8192, Effort: effort, ReasoningStyle: model.EffectiveReasoningStyle(),
		}, stream)
		if err != nil {
			if ctx.Err() != nil {
				s.emit("interrupt", map[string]string{"reason": ctx.Err().Error(), "partial": reply.Text})
			}
			return reply, err
		}
		s.messages = append(s.messages, babel.Message{
			Role: "assistant", Content: reply.Text, ToolCalls: reply.ToolCalls,
		})
		s.emit("transmission", map[string]string{"text": reply.Text, "reasoning": reply.Reasoning})
		if len(reply.ToolCalls) == 0 {
			return reply, nil
		}
		results := s.runToolBatch(ctx, reply.ToolCalls)
		for i, call := range reply.ToolCalls {
			result := results[i]
			s.messages = append(s.messages, babel.Message{
				Role: "tool", ToolCallID: call.ID, Content: result.Content,
			})
			s.emit("probe_result", map[string]any{
				"tool_call_id": call.ID, "tool": call.Function.Name,
				"content": result.Content, "error": result.IsError,
			})
		}
	}
	return babel.Reply{}, fmt.Errorf("unimatrix: tool loop exceeded %d cycles", s.MaxCycles)
}

const maxParallelReadOnlyTools = 6

func (s *Session) runToolBatch(ctx context.Context, calls []babel.ToolCall) []tools.Result {
	results := make([]tools.Result, len(calls))
	for i := 0; i < len(calls); {
		if s.canRunParallelReadOnly(ctx, calls[i]) {
			end := i + 1
			for end < len(calls) && (end-i) < maxParallelReadOnlyTools && s.canRunParallelReadOnly(ctx, calls[end]) {
				end++
			}
			if end-i > 1 {
				var wg sync.WaitGroup
				for j := i; j < end; j++ {
					wg.Add(1)
					go func(idx int, call babel.ToolCall) {
						defer wg.Done()
						results[idx] = s.runTool(ctx, call)
					}(j, calls[j])
				}
				wg.Wait()
				i = end
				continue
			}
		}
		results[i] = s.runTool(ctx, calls[i])
		i++
	}
	return results
}

func (s *Session) canRunParallelReadOnly(ctx context.Context, call babel.ToolCall) bool {
	if s.Registry == nil || s.Gate == nil {
		return false
	}
	tool, ok := s.Registry.Lookup(call.Function.Name)
	if !ok || !tool.ReadOnly() {
		return false
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return false
	}
	return s.Gate.Decide(ctx, tool, args) == queen.Allow
}

func (s *Session) runTool(ctx context.Context, call babel.ToolCall) tools.Result {
	tool, ok := s.Registry.Lookup(call.Function.Name)
	if !ok {
		return tools.Result{IsError: true, Content: "unknown tool " + call.Function.Name, Summary: "tool · unknown"}
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return tools.Result{IsError: true, Content: "invalid tool arguments: " + err.Error(), Summary: "tool · invalid arguments"}
	}
	decision := s.Gate.Decide(ctx, tool, args)
	if decision == queen.Deny {
		return tools.Result{IsError: true, Content: "Queen denied this tool call", Summary: "tool · denied"}
	}
	if decision == queen.Ask && (s.Approve == nil || !s.Approve(ctx, call)) {
		return tools.Result{IsError: true, Content: "User declined this tool call", Summary: "tool · declined"}
	}
	return tools.Execute(ctx, tool, args)
}

func (s *Session) emit(kind string, body any) {
	if s.OnEvent == nil {
		return
	}
	drone, err := history.NewDrone(kind, s.ID, body)
	if err == nil {
		s.OnEvent(*drone)
	}
}
