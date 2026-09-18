package science

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"annorax/internal/history"
	"annorax/internal/tools"
)

type Artifact struct {
	ID        string            `json:"id"`
	Path      string            `json:"path"`
	Hash      string            `json:"hash"`
	Tier      tools.StorageTier `json:"tier"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
	Stale     bool              `json:"stale"`
	Consumes  []string          `json:"consumes,omitempty"`
}

func NewArtifact(path string) (*Artifact, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	tier := tools.StorageTierFor(path)
	artifact := &Artifact{
		ID: "artifact_" + hex.EncodeToString(sum[:8]), Path: path,
		Hash: hex.EncodeToString(sum[:]), Tier: tier,
	}
	if expiry := tier.ExpiresAt(time.Now()); !expiry.IsZero() {
		artifact.ExpiresAt = &expiry
	}
	return artifact, nil
}

func (a Artifact) Drone(sessionID string) (*history.Drone, error) {
	drone, err := history.NewDrone("artifact", sessionID, a)
	if err != nil {
		return nil, err
	}
	for _, input := range a.Consumes {
		drone.Links = append(drone.Links, history.Link{
			SourceID: drone.ID, TargetID: input, Relation: "consumes", Kind: "artifact",
			CreatedAt: time.Now(),
		})
	}
	return drone, nil
}

// StaleClosure returns every downstream artifact invalidated by changed IDs.
func StaleClosure(artifacts map[string]*Artifact, changed ...string) ([]string, error) {
	queue := append([]string(nil), changed...)
	stale := map[string]bool{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, artifact := range artifacts {
			for _, input := range artifact.Consumes {
				if input == id && !stale[artifact.ID] {
					stale[artifact.ID] = true
					artifact.Stale = true
					queue = append(queue, artifact.ID)
				}
			}
		}
	}
	out := make([]string, 0, len(stale))
	for id := range stale {
		if artifacts[id] == nil {
			return nil, fmt.Errorf("science: missing downstream artifact %q", id)
		}
		out = append(out, id)
	}
	return out, nil
}
