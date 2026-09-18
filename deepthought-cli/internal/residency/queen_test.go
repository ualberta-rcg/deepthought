package residency

import (
	"context"
	"testing"
	"time"

	"annorax/internal/history"
)

func TestQueenNeverDemotesPinsOrAnnotations(t *testing.T) {
	now := time.Now()
	for _, kind := range []string{"annotation", "probe_result"} {
		drone, _ := history.NewDrone(kind, "session", map[string]string{"x": "y"})
		drone.CreatedAt = now.Add(-365 * 24 * time.Hour)
		drone.Pinned = kind != "annotation"
		decision := (Queen{}).Decide(context.Background(), drone, now)
		if decision.To != history.StateFull {
			t.Fatalf("%s demoted to %s", kind, decision.To)
		}
	}
}
