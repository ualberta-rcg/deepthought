package assimilation

import (
	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/history"
	"encoding/json"
	"fmt"
)

// Wire budgets an already valid conversation without breaking tool-call pairs.
// It keeps user instructions and the current exchange intact; older bulky tool
// output is a labelled preview. If that is insufficient the caller must compact
// or choose a larger model, rather than silently losing active constraints.
func Wire(messages []babel.Message, tools []babel.ToolDef, window, output int, references map[string]string, pinned ...map[string]bool) ([]babel.Message, Manifest, error) {
	if window <= 0 {
		window = 32768
	}
	reserve := max(256, window/20)
	budget := window - output - reserve
	manifest := Manifest{Budget: budget}
	out := append([]babel.Message(nil), messages...)
	lastUser := -1
	for i, msg := range out {
		if msg.Role == "user" {
			lastUser = i
		}
	}
	estimate := func() int {
		n := 0
		for _, msg := range out {
			raw, _ := json.Marshal(msg)
			n += estimateText(string(raw)) + 8
		}
		raw, _ := json.Marshal(tools)
		return n + estimateText(string(raw))
	}
	if budget <= 0 {
		return nil, manifest, fmt.Errorf("context window %d cannot fit output reserve %d; lower max_tokens", window, output)
	}
	for i := range out {
		if estimate() <= budget {
			break
		}
		if i >= lastUser || out[i].Role != "tool" || len(out[i].Content) <= 2048 {
			continue
		}
		id := references[out[i].ToolCallID]
		if len(pinned) > 0 && pinned[0][id] {
			continue
		}
		if id == "" {
			id = out[i].ToolCallID
		}
		out[i].Content = history.Condense(out[i].Content, 16, 1536) + "\n[Earlier tool output shortened; use expand with history ID " + id + " for the full result.]"
	}
	manifest.Estimated = estimate()
	for i, msg := range out {
		id := references[msg.ToolCallID]
		if id == "" {
			id = fmt.Sprintf("message:%d", i)
		}
		state, reason := history.StateFull, "included"
		if msg.Content != messages[i].Content {
			state = history.StateDigest
			reason = "older tool output bounded"
		}
		if msg.Role == "system" || msg.Role == "user" || i >= lastUser {
			reason = "instructions or active exchange preserved"
		}
		raw, _ := json.Marshal(msg)
		manifest.Entries = append(manifest.Entries, ManifestEntry{ID: id, Kind: msg.Role, State: state, Tokens: estimateText(string(raw)) + 8, Reason: reason})
	}
	if manifest.Estimated > budget {
		return nil, manifest, fmt.Errorf("context needs approximately %d tokens; budget is %d after tool schemas and output reserve. Use /compact or choose a larger context model", manifest.Estimated, budget)
	}
	return out, manifest, nil
}
