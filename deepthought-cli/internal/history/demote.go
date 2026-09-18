package history

import (
	"fmt"
)

// DemoteProbe is the callable seam by which a future Queen AI thread shortens an
// old, bulky tool result so it stops costing context. It is NOT called from the
// chat loop — nothing demotes automatically. The flow matches the user's mental
// model: "set it to tombstone → it makes the tombstone, then sets the state."
//
// Concretely: load the probe, fill the deterministic summary the target level
// needs (a tombstone marker, or a truncated digest) if it is empty, set the
// probe's residency State, and persist. The next GetCollective rehydrates the
// State onto the in-memory node and MessagesByState renders the shorter view.
//
// LLM-generated summaries (Semantic) and a budget-aware decider (residency/
// assimilation) are the future Queen thread's job; this function only provides
// the deterministic, no-network path.
func DemoteProbe(store ChatStore, collectiveID, probeID string, to State) error {
	if store == nil {
		return fmt.Errorf("history: DemoteProbe requires a store")
	}
	coll, err := store.GetCollective(collectiveID)
	if err != nil {
		return fmt.Errorf("history: demote: %w", err)
	}
	probe := findProbeByID(coll, probeID)
	if probe == nil {
		return fmt.Errorf("history: probe %q not found in %s", probeID, collectiveID)
	}
	ensureProbeSummary(probe, to)
	// Monotonic: never promote. stateIndex orders Full<Digest<Line<Tombstone<Elided>.
	if stateIndex(to) < stateIndex(probe.State) {
		return fmt.Errorf("history: cannot demote %s → %s (would promote)", probe.State, to)
	}
	probe.State = to
	probe.Touch()
	return store.SaveObject(probe)
}

// findProbeByID walks a collective's graph for a probe by its Vinculum ID.
func findProbeByID(coll *Collective, probeID string) *Probe {
	for _, inc := range coll.Incursions {
		for _, tx := range inc.Transmissions {
			for _, p := range tx.Probes {
				if p.ID == probeID {
					return p
				}
			}
		}
	}
	return nil
}

// ensureProbeSummary fills the deterministic summary the target residency needs,
// if it is empty, so MessagesByState actually renders something shorter rather
// than falling back to Full (SummarySet.At falls back toward richer levels).
func ensureProbeSummary(p *Probe, to State) {
	switch to {
	case StateTombstone:
		if p.Result.Summaries.Tombstone == "" {
			p.Result.Summaries.Tombstone = "[summarized]"
		}
	case StateDigest:
		if p.Result.Summaries.Condensed == "" && p.Result.Content != "" {
			r := []rune(p.Result.Content)
			if len(r) > 200 {
				p.Result.Summaries.Condensed = string(r[:200]) + "…"
			} else {
				p.Result.Summaries.Condensed = p.Result.Content
			}
		}
	}
}
