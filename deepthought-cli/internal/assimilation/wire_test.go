package assimilation

import (
	"deepthought-cli/internal/babel"
	"strings"
	"testing"
)

func TestWireBudgetPreservesToolPairs(t *testing.T) {
	msgs := []babel.Message{{Role: "system", Content: "rules"}, {Role: "user", Content: "earlier request"}, {Role: "assistant", ToolCalls: []babel.ToolCall{{ID: "call", Type: "function", Function: babel.FunctionCall{Name: "read", Arguments: `{}`}}}}, {Role: "tool", ToolCallID: "call", Content: strings.Repeat("large output ", 2000)}, {Role: "user", Content: "current constraints"}}
	out, manifest, err := Wire(msgs, nil, 4096, 512, map[string]string{"call": "probe_123"})
	if err != nil {
		t.Fatal(err)
	}
	if out[3].ToolCallID != "call" || out[2].ToolCalls[0].ID != "call" || out[4].Content != "current constraints" {
		t.Fatal("conversation pairing changed")
	}
	if !strings.Contains(out[3].Content, "probe_123") || manifest.Estimated > manifest.Budget {
		t.Fatal("missing recoverable preview or exceeded budget")
	}
	if len(msgs[3].Content) < 20000 {
		t.Fatal("modified original messages")
	}
}
func TestWireRefusesToDropRequiredInstructions(t *testing.T) {
	_, _, err := Wire([]babel.Message{{Role: "system", Content: strings.Repeat("required ", 2000)}}, nil, 2048, 512, nil)
	if err == nil {
		t.Fatal("silently discarded instructions")
	}
}
