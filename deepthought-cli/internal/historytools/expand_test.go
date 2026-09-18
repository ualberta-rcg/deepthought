package historytools

import (
	"context"
	"strings"
	"testing"

	"annorax/internal/history"
)

type fakeReader struct{ drone *history.Drone }

func (f fakeReader) GetDrone(context.Context, string) (*history.Drone, error) { return f.drone, nil }

func TestExpandRecoversFullBody(t *testing.T) {
	drone, _ := history.NewDrone("probe_result", "session", map[string]string{"output": "full"})
	result := NewExpand(fakeReader{drone}).Run(context.Background(), map[string]any{"id": drone.ID})
	if result.IsError || !strings.Contains(result.Content, `"full"`) {
		t.Fatalf("expand = %+v", result)
	}
}
