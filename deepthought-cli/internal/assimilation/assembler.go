// Package assimilation selects and budgets Drone context.
package assimilation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/history"
)

type ManifestEntry struct {
	ID       string        `json:"id"`
	Kind     string        `json:"kind"`
	State    history.State `json:"state"`
	Tokens   int           `json:"tokens"`
	Priority int           `json:"priority"`
	Reason   string        `json:"reason"`
}

type Manifest struct {
	Budget    int             `json:"budget"`
	Estimated int             `json:"estimated"`
	Entries   []ManifestEntry `json:"entries"`
}

type Result struct {
	Messages []babel.Message
	Manifest Manifest
	Drone    *history.Drone
}

type Assembler struct {
	Handlers *history.HandlerRegistry
}

type candidate struct {
	drone    *history.Drone
	state    history.State
	priority int
	reason   string
	text     string
	tokens   int
}

// Assemble applies pinned/annotation/recent/relevant/plan ordering, then
// demotes the lowest-priority candidates until the high-biased estimate fits.
func (a Assembler) Assemble(ctx context.Context, sessionID, goal string, drones []*history.Drone, budget int) (Result, error) {
	if budget <= 0 {
		return Result{}, fmt.Errorf("assimilation: token budget must be positive")
	}
	candidates := make([]candidate, 0, len(drones))
	for i, drone := range drones {
		if drone == nil {
			continue
		}
		priority, reason := score(drone, i, len(drones), goal)
		state := drone.State
		if state == "" {
			state = history.StateFull
		}
		if drone.Pinned || drone.Kind == "annotation" {
			state = history.StateFull
		}
		candidates = append(candidates, candidate{drone: drone, state: state, priority: priority, reason: reason})
	}
	if err := a.renderAll(ctx, candidates); err != nil {
		return Result{}, err
	}
	for estimate(candidates) > budget {
		at := demotionCandidate(candidates)
		if at < 0 {
			break
		}
		candidates[at].state = nextState(candidates[at].state)
		text, err := a.render(ctx, candidates[at].drone, candidates[at].state)
		if err != nil {
			return Result{}, err
		}
		candidates[at].text = text
		candidates[at].tokens = estimateText(text)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].drone.CreatedAt.Before(candidates[j].drone.CreatedAt)
	})
	result := Result{Manifest: Manifest{Budget: budget}}
	for _, item := range candidates {
		if item.state != history.StateElided && item.text != "" {
			result.Messages = append(result.Messages, babel.Message{
				Role: roleFor(item.drone.Kind), Content: item.text,
			})
		}
		result.Manifest.Entries = append(result.Manifest.Entries, ManifestEntry{
			ID: item.drone.ID, Kind: item.drone.Kind, State: item.state,
			Tokens: item.tokens, Priority: item.priority, Reason: item.reason,
		})
		result.Manifest.Estimated += item.tokens
	}
	manifestDrone, err := history.NewDrone("manifest", sessionID, result.Manifest)
	if err != nil {
		return Result{}, err
	}
	result.Drone = manifestDrone
	return result, nil
}

func score(drone *history.Drone, index, total int, goal string) (int, string) {
	switch {
	case drone.Pinned:
		return 1000, "pinned"
	case drone.Kind == "annotation":
		return 950, "annotation"
	case index >= total-8:
		return 800 + index, "recent"
	case hasPlanLink(drone):
		return 700, "active plan"
	case goal != "" && strings.Contains(strings.ToLower(string(drone.Body)), strings.ToLower(goal)):
		return 600, "goal text"
	default:
		return 100, "background"
	}
}

func hasPlanLink(drone *history.Drone) bool {
	for _, link := range drone.Links {
		switch link.Relation {
		case "depends_on", "attempt_of", "breaks_into", "references":
			return true
		}
	}
	return false
}

func (a Assembler) renderAll(ctx context.Context, items []candidate) error {
	for i := range items {
		text, err := a.render(ctx, items[i].drone, items[i].state)
		if err != nil {
			return err
		}
		items[i].text, items[i].tokens = text, estimateText(text)
	}
	return nil
}

func (a Assembler) render(ctx context.Context, drone *history.Drone, state history.State) (string, error) {
	if a.Handlers != nil {
		if handler, ok := a.Handlers.Handler(drone.Kind); ok {
			return handler.Render(ctx, drone, state)
		}
	}
	switch state {
	case history.StateFull:
		if drone.Summaries.Full != "" {
			return drone.Summaries.Full, nil
		}
		return string(drone.Body), nil
	case history.StateDigest:
		return drone.Summaries.At(history.SummaryCondensed), nil
	case history.StateLine:
		return drone.Summaries.At(history.SummarySemantic), nil
	case history.StateTombstone:
		if drone.Summaries.Tombstone != "" {
			return drone.Summaries.Tombstone, nil
		}
		return fmt.Sprintf("[%s %s tombstoned; use expand(%q)]", drone.Kind, drone.ID, drone.ID), nil
	case history.StateElided:
		return "", nil
	default:
		return string(drone.Body), nil
	}
}

func estimate(items []candidate) int {
	total := 0
	for _, item := range items {
		total += item.tokens
	}
	return total
}

// estimateText is anchor-and-estimate: no provider-specific tokenizer, and a
// 4/3 safety bias over the conventional four-characters-per-token estimate.
func estimateText(text string) int {
	return int(math.Ceil(float64(len([]rune(text))) / 4.0 * 4.0 / 3.0))
}

func demotionCandidate(items []candidate) int {
	at := -1
	for i := range items {
		if items[i].drone.Pinned || items[i].drone.Kind == "annotation" || items[i].state == history.StateElided {
			continue
		}
		if at < 0 || items[i].priority < items[at].priority {
			at = i
		}
	}
	return at
}

func nextState(state history.State) history.State {
	switch state {
	case history.StateFull:
		return history.StateDigest
	case history.StateDigest:
		return history.StateLine
	case history.StateLine:
		return history.StateTombstone
	default:
		return history.StateElided
	}
}

func roleFor(kind string) string {
	switch kind {
	case "hail":
		return "user"
	case "transmission":
		return "assistant"
	case "probe_result":
		return "tool"
	default:
		return "system"
	}
}

// MarshalManifest gives /vortex and audit exporters a stable representation.
func MarshalManifest(manifest Manifest) []byte {
	raw, _ := json.Marshal(manifest)
	return raw
}
