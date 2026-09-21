package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Skill is a read-only tool that loads the full instructions of an available
// skill by name (progressive disclosure). The model sees only the compact skill
// index in its system prompt; it calls this to pull the body of the one skill
// relevant to the current task. Loading a skill has no side effects, so it is
// ReadOnly (Queen auto-allows it, no prompt).
//
// The tool is deliberately decoupled from the skills package (which would create
// an import cycle): it is handed a list of names and a lookup callback that
// returns a skill body. main wires the callback to the loaded skill set.
type Skill struct {
	LiveNames  func() []string
	LiveLookup func(string) (string, []string, bool, error)
	names      []string // sorted, for stable listing
	lookup     func(name string) (body string, found bool, err error)
}

// NewSkillTool builds the skill tool. `names` is the set of skill names and
// `lookup` resolves a name to its full body (progressive disclosure).
func NewSkillTool(names []string, lookup func(name string) (string, bool, error)) *Skill {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return &Skill{names: sorted, lookup: lookup}
}

func (t *Skill) Name() string { return "skill" }

func (t *Skill) Description() string {
	return "Load the full instructions for an available skill by name. Your system prompt " +
		"lists the available skills; call this with a skill's name to read its complete " +
		"guidance before doing a task that skill covers. Call it with no name to list the " +
		"available skills."
}

func (t *Skill) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The skill's name (e.g. \"alliance-slurm\"). Omit to list available skills.",
			},
		},
	}
}

func (t *Skill) ReadOnly() bool { return true }

func (t *Skill) Run(_ context.Context, args map[string]any) Result {
	names := t.names
	if t.LiveNames != nil {
		names = t.LiveNames()
	}
	name := strings.TrimSpace(stringArg(args, "name"))
	if name == "" {
		return Result{
			Content: "Available skills:\n" + strings.Join(names, "\n"),
			Summary: fmt.Sprintf("skill: %d available", len(names)),
		}
	}
	var body string
	var found bool
	var err error
	var allowed []string
	if t.LiveLookup != nil {
		body, allowed, found, err = t.LiveLookup(name)
	} else {
		body, found, err = t.lookup(name)
	}
	if !found {
		return Result{
			Content: fmt.Sprintf("No skill named %q. Available skills:\n%s", name, strings.Join(names, "\n")),
			IsError: true,
			Summary: "skill: not found",
		}
	}
	if err != nil {
		return Result{IsError: true, Content: "failed to load skill: " + err.Error(), Summary: "skill: load error"}
	}
	return Result{
		AllowedTools: allowed,
		Content:      "# Skill: " + name + "\n\n" + body,
		Summary:      "skill: " + name,
	}
}

// stringArg pulls a string (or nothing) out of a tool-args map.
func stringArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}
