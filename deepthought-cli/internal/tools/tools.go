// Package tools holds DeepThought's native tool implementations and the registry that
// advertises them to the model. A Tool is a capability the agent can invoke: bash
// (run a command), read (read a file), and later edit/glob/grep/etc. Each tool owns
// its JSON-schema (sent in the request `tools` array) and its Run; the TUI loop and
// the Queen gate call into these.
//
// Result is deliberately split: Content is what the model sees (full stdout, line-
// numbered file text); Summary is one-line chrome for the transcript (NOT sent to the
// model). This mirrors the reference's separation of model payload from TUI render.
package tools

import (
	"context"
	"sort"

	"deepthought-cli/internal/babel"
)

// Result is what a Tool.Run returns. Content feeds the model; Summary feeds the TUI.
type Result struct {
	Content string // sent to the model as the role:"tool" message body
	IsError bool   // flags a failed/permission-denied result in the transcript
	Summary string // one-line TUI chrome; never sent to the model
}

// Tool is one native capability. Implementations are stateless and concurrency-safe
// (a shared Registry serves all sessions); per-call state lives in Run's args.
type Tool interface {
	Name() string
	Description() string        // model-facing prose
	Parameters() map[string]any // JSON Schema for the function "parameters"
	ReadOnly() bool             // true = safe to auto-allow (Queen never asks)
	Run(ctx context.Context, args map[string]any) Result
}

// Registry is the set of tools advertised to the model. The zero value is not usable;
// build with NewRegistry.
type Registry struct {
	byName map[string]Tool
	order  []string // stable iteration order for cache-friendly tool arrays
}

// NewRegistry builds a registry holding the given tools. Duplicate names keep the
// last one. The order of tools in Schemas() follows the order passed here.
func NewRegistry(ts ...Tool) *Registry {
	r := &Registry{byName: map[string]Tool{}}
	for _, t := range ts {
		if _, ok := r.byName[t.Name()]; !ok {
			r.order = append(r.order, t.Name())
		}
		r.byName[t.Name()] = t
	}
	return r
}

// Lookup finds a tool by the name the model used in its tool_call.
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Schemas renders the registry as the OpenAI request `tools` array, in stable order
// so the request body is byte-stable across turns (helps server-side prompt caching).
func (r *Registry) Schemas() []babel.ToolDef {
	if r == nil || len(r.order) == 0 {
		return nil
	}
	out := make([]babel.ToolDef, 0, len(r.order))
	for _, name := range r.order {
		t := r.byName[name]
		out = append(out, babel.NewToolDef(t.Name(), t.Description(), t.Parameters()))
	}
	return out
}

// Names returns the registered tool names in stable order (handy for prompts/tests).
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}
