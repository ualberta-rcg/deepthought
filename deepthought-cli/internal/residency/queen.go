// Package residency implements the Queen's tiered context-residency policy.
package residency

import (
	"context"
	"fmt"
	"time"

	"deepthought-cli/internal/history"
)

type Decision struct {
	DroneID string        `json:"drone_id"`
	From    history.State `json:"from"`
	To      history.State `json:"to"`
	Reason  string        `json:"reason"`
	Tier    int           `json:"tier"`
}

type Policy interface {
	Decide(context.Context, *history.Drone, time.Time) Decision
}

type Queen struct {
	DigestAfter, TombstoneAfter time.Duration
	LargeBody                   int
}

func (p Queen) Decide(_ context.Context, drone *history.Drone, now time.Time) Decision {
	decision := Decision{DroneID: drone.ID, From: drone.State, To: drone.State, Tier: 1}
	if drone.Kind == "annotation" {
		decision.Reason = "annotation invariant"
		return decision
	}
	if drone.Pinned {
		decision.Reason = "pinned invariant"
		return decision
	}
	if p.LargeBody <= 0 {
		p.LargeBody = 256 << 10
	}
	if p.DigestAfter <= 0 {
		p.DigestAfter = 24 * time.Hour
	}
	if p.TombstoneAfter <= 0 {
		p.TombstoneAfter = 30 * 24 * time.Hour
	}
	age := now.Sub(drone.CreatedAt)
	switch {
	case age >= p.TombstoneAfter:
		decision.To, decision.Reason = history.StateTombstone, "age threshold"
	case len(drone.Body) >= p.LargeBody:
		decision.To, decision.Reason = history.StateDigest, "large body"
	case age >= p.DigestAfter:
		decision.To, decision.Reason = history.StateDigest, "age threshold"
	default:
		decision.Reason = "resident"
	}
	return decision
}

func DecisionDrone(sessionID string, decision Decision) (*history.Drone, error) {
	drone, err := history.NewDrone("queen_decision", sessionID, decision)
	if err != nil {
		return nil, fmt.Errorf("queen: decision drone: %w", err)
	}
	return drone, nil
}
