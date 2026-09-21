package history

import (
	"reflect"
	"testing"

	"deepthought-cli/internal/babel"
)

// TestMessagesByStateIdenticalToFullWhenFull asserts the state-aware flatten is
// byte-identical to Messages(SummaryFull) when every node is at the default
// residency — i.e. wiring the seam changes nothing about what the model sees
// today.
func TestMessagesByStateIdenticalToFullWhenFull(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SystemPrompt: "sys", SessionID: "s"})
	inc := coll.StartIncursion("hello")
	tx, _ := inc.AddTransmission("Hi", []babel.ToolCall{{
		ID: "c1", Type: "function", Function: babel.FunctionCall{Name: "read", Arguments: `{}`},
	}})
	tx.Probes[0].Result = ResultView{Content: "body", Summaries: SummarySet{Full: "body"}}
	tx.Probes[0].Status = ProbeCompleted
	inc.MarkCompleted()

	want := coll.Messages(SummaryFull)
	got := coll.MessagesByState()
	if len(want) != len(got) {
		t.Fatalf("len want=%d got=%d", len(want), len(got))
	}
	for i := range want {
		if !reflect.DeepEqual(want[i], got[i]) {
			t.Fatalf("msg[%d] differs:\n want=%+v\n got =%+v", i, want[i], got[i])
		}
	}
}

// TestMessagesByStateRendersDemotedProbe asserts that once a probe's State is
// Tombstone (and its Tombstone summary populated), the state-aware flatten shows
// the tombstone, not the full body — the mechanism a future Queen thread drives.
func TestMessagesByStateRendersDemotedProbe(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SystemPrompt: "sys", SessionID: "s"})
	inc := coll.StartIncursion("hello")
	tx, _ := inc.AddTransmission("Hi", []babel.ToolCall{{
		ID: "c1", Type: "function", Function: babel.FunctionCall{Name: "read", Arguments: `{}`},
	}})
	tx.Probes[0].Result = ResultView{
		Content:   "big body",
		Summaries: SummarySet{Full: "big body", Tombstone: "[summarized]"},
	}
	tx.Probes[0].State = StateTombstone
	tx.Probes[0].Status = ProbeCompleted
	inc.MarkCompleted()

	msgs := coll.MessagesByState()
	// system, user, assistant, tool
	var toolContent string
	for _, m := range msgs {
		if m.Role == "tool" {
			toolContent = m.Content
		}
	}
	if toolContent != "[summarized]" {
		t.Fatalf("tool content=%q want [summarized]", toolContent)
	}
}
