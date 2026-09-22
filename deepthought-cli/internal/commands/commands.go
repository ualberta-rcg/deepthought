// Package commands defines DeepThought's slash-command registry.
package commands

import (
	"sort"
	"strings"
)

type Kind string

const (
	Local  Kind = "local"
	Screen Kind = "screen"
	Prompt Kind = "prompt"
)

type Command struct {
	Name         string
	Description  string
	Kind         Kind
	Aliases      []string
	ArgumentHint string
	WhenToUse    string
	Hidden       bool
	Immediate    bool
}

type Registry struct {
	commands []Command
}

func New(items ...Command) *Registry {
	return &Registry{commands: append([]Command(nil), items...)}
}

func Builtins() *Registry {
	names := []struct {
		name, description string
		kind              Kind
	}{
		{"help", "show commands and keys", Local},
		{"menu", "open navigation without function keys", Local},
		{"doctor", "check local configuration and context", Local},
		{"compact", "shorten older tool output while preserving originals", Local},
		{"pin", "preserve a probe in context by history ID", Local},
		{"unpin", "remove a probe context pin", Local},
		{"memory", "manage explicitly approved reference facts", Local},
		{"export", "export histories to a directory", Local},
		{"model", "switch the active model", Local},
		{"effort", "show or set reasoning effort", Local},
		{"settings", "open settings", Screen},
		{"status", "open the status page (F12)", Screen},
		{"models", "manage the model catalog (F11)", Screen},
		{"cron", "manage your crontab (F8)", Screen},
		{"jobs", "track submissions and review retry advice", Screen},
		{"plan", "saved scientific plans and validation evidence", Screen},
		{"context", "inspect the context manifest", Screen},
		{"import", "review discovered providers before connecting", Local},
		{"resume", "continue an interrupted turn", Local},
		{"quit", "exit DeepThought", Local},
	}
	out := make([]Command, 0, len(names))
	for _, item := range names {
		out = append(out, Command{Name: item.name, Description: item.description, Kind: item.kind})
	}
	out[0].Aliases = []string{"guide", "?"}
	for i := range out {
		if out[i].Name == "context" {
			out[i].Aliases = []string{"vortex"}
		}
	}
	out[len(out)-1].Aliases = []string{"fish", "exit"}
	return New(out...)
}

func (r *Registry) Lookup(name string) (Command, bool) {
	name = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "/")
	for _, command := range r.commands {
		if command.Name == name {
			return command, true
		}
		for _, alias := range command.Aliases {
			if alias == name {
				return command, true
			}
		}
	}
	return Command{}, false
}

// Search uses a small subsequence score, preferring prefix and shorter names.
func (r *Registry) Search(query string, limit int) []Command {
	query = strings.TrimPrefix(strings.ToLower(query), "/")
	type scored struct {
		command Command
		score   int
	}
	var matches []scored
	for _, command := range r.commands {
		if command.Hidden {
			continue
		}
		score, ok := fuzzyScore(command.Name, query)
		if ok {
			matches = append(matches, scored{command: command, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].command.Name < matches[j].command.Name
		}
		return matches[i].score > matches[j].score
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]Command, len(matches))
	for i := range matches {
		out[i] = matches[i].command
	}
	return out
}

func fuzzyScore(candidate, query string) (int, bool) {
	if query == "" {
		return 0, true
	}
	if strings.HasPrefix(candidate, query) {
		return 1000 - len(candidate), true
	}
	at, score := 0, 0
	for _, want := range query {
		found := false
		for at < len(candidate) {
			if rune(candidate[at]) == want {
				score += 10 - at
				at++
				found = true
				break
			}
			at++
		}
		if !found {
			return 0, false
		}
	}
	return score, true
}
