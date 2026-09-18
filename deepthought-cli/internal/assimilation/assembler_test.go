package assimilation

import (
	"context"
	"strings"
	"testing"

	"annorax/internal/history"
)

func TestAssemblerPreservesPinsAndDemotesBackground(t *testing.T) {
	pinned, _ := history.NewDrone("hail", "session", map[string]string{"text": strings.Repeat("p", 100)})
	pinned.Pinned = true
	pinned.Summaries.Full = strings.Repeat("p", 100)
	old, _ := history.NewDrone("probe_result", "session", map[string]string{"output": strings.Repeat("x", 2000)})
	old.Summaries = history.SummarySet{
		Full: strings.Repeat("x", 2000), Condensed: "digest", Semantic: "line",
		Tombstone: "large output succeeded",
	}
	result, err := (Assembler{}).Assemble(context.Background(), "session", "", []*history.Drone{old, pinned}, 40)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]history.State{}
	for _, entry := range result.Manifest.Entries {
		states[entry.ID] = entry.State
	}
	if states[pinned.ID] != history.StateFull {
		t.Fatal("pin was demoted")
	}
	if states[old.ID] == history.StateFull {
		t.Fatal("background was not demoted")
	}
	if result.Drone == nil || result.Drone.Kind != "manifest" {
		t.Fatal("manifest drone missing")
	}
}
