package history

// DronesFromCollective converts the v0 nested graph into flat records. It is
// deterministic and side-effect free, so stores can run it once on legacy
// JSONL transcripts and mark the migration complete.
func DronesFromCollective(coll *Collective) ([]*Drone, error) {
	if coll == nil {
		return nil, nil
	}
	var out []*Drone
	add := func(entity Entity, body any, summaries SummarySet) error {
		v := entity.GetVinculum()
		drone, err := NewDrone(v.Kind, v.SessionID, body)
		if err != nil {
			return err
		}
		drone.Vinculum = *v
		drone.Summaries = summaries
		drone.State = StateFull
		out = append(out, drone)
		return nil
	}
	if err := add(coll, map[string]any{
		"system_prompt": coll.SystemPrompt, "title": coll.Title,
		"max_cycles": coll.MaxCycles, "cycles": coll.Cycles,
	}, SummarySet{Full: coll.Title}); err != nil {
		return nil, err
	}
	for _, inc := range coll.Incursions {
		if err := add(inc, map[string]any{
			"status": inc.Status.String(), "prompt": inc.Prompt,
			"cycles": inc.Cycles, "error": inc.Error,
		}, inc.Summaries); err != nil {
			return nil, err
		}
		for _, tx := range inc.Transmissions {
			if err := add(tx, map[string]any{"text": tx.Text}, SummarySet{Full: tx.Text}); err != nil {
				return nil, err
			}
			for _, probe := range tx.Probes {
				if err := add(probe, map[string]any{
					"wire_id": probe.WireID, "name": probe.Name,
					"arguments": probe.Arguments, "status": probe.Status.String(),
					"result": probe.Result.Content,
				}, probe.Summaries); err != nil {
					return nil, err
				}
				for _, pattern := range probe.Patterns {
					if err := add(pattern, map[string]any{
						"category": pattern.Category, "content": pattern.Content,
						"source_probe_id": pattern.SourceProbeID,
					}, SummarySet{Full: pattern.Content}); err != nil {
						return nil, err
					}
				}
			}
			for _, synapse := range tx.Synapses {
				if err := add(synapse, map[string]any{
					"content": synapse.Content, "type": synapse.Type,
				}, SummarySet{Full: synapse.Content}); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}
