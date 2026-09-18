package historytools

import (
	"context"
	"fmt"
	"strings"

	"annorax/internal/history"
	"annorax/internal/tools"
)

type DroneReader interface {
	GetDrone(context.Context, string) (*history.Drone, error)
}

type Expand struct{ store DroneReader }

func NewExpand(store DroneReader) *Expand { return &Expand{store: store} }
func (*Expand) Name() string              { return "expand" }
func (*Expand) Description() string {
	return "Recover the full immutable body of a tombstoned or summarized interaction by ID."
}
func (*Expand) Parameters() map[string]any {
	return map[string]any{
		"type": "object", "properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "Drone interaction ID"},
		},
		"required": []string{"id"}, "additionalProperties": false,
	}
}
func (*Expand) ReadOnly() bool { return true }
func (e *Expand) Run(ctx context.Context, args map[string]any) tools.Result {
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return tools.Result{IsError: true, Content: "expand: missing id", Summary: "expand · missing id"}
	}
	if e.store == nil {
		return tools.Result{IsError: true, Content: "expand: history store unavailable", Summary: "expand · unavailable"}
	}
	drone, err := e.store.GetDrone(ctx, id)
	if err != nil {
		return tools.Result{IsError: true, Content: "expand: " + err.Error(), Summary: "expand · not found"}
	}
	return tools.Result{
		Content: string(drone.Body),
		Summary: fmt.Sprintf("expand %s · %d bytes", id, len(drone.Body)),
	}
}
