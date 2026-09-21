package history

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"deepthought-cli/internal/babel"
)

// Messages flattens the collective into Babel wire messages at the chosen
// summary level. The system prompt is always prepended first. Failed incursions
// are skipped. Tool-call IDs are preserved verbatim so OpenAI-compatible backends
// can pair assistant tool_use blocks with their tool results.
func (c *Collective) Messages(level SummaryLevel) []babel.Message {
	return c.messagesAt(func(*Incursion, *Transmission, *Probe) SummaryLevel { return level })
}

// MessagesByState flattens with each probe rendered at its own residency
// (defaulting Full). Today every State is ""/Full, so output is byte-identical
// to Messages(SummaryFull); once a future Queen AI thread demotes a probe and
// GetCollective rehydrates its State, that probe renders at the demoted level.
// Transmissions always render their full text (they have no SummarySet yet).
func (c *Collective) MessagesByState() []babel.Message {
	return c.messagesAt(func(_ *Incursion, _ *Transmission, p *Probe) SummaryLevel {
		return stateToLevel(p.State)
	})
}

// messagesAt is the shared flatten: level returns the summary level for a given
// probe (Messages passes a constant; MessagesByState consults the probe's State).
func (c *Collective) messagesAt(level func(*Incursion, *Transmission, *Probe) SummaryLevel) []babel.Message {
	out := make([]babel.Message, 0, len(c.Incursions)*3+1)
	out = append(out, babel.Message{Role: "system", Content: c.SystemPrompt})

	for _, inc := range c.Incursions {
		if inc.Status == IncursionFailed && len(inc.Transmissions) == 0 {
			continue
		}

		out = append(out, babel.Message{Role: "user", Content: inc.Prompt})

		for _, tx := range inc.Transmissions {
			msg := babel.Message{
				Role:    "assistant",
				Content: tx.Text,
			}
			if len(tx.Probes) > 0 {
				msg.ToolCalls = make([]babel.ToolCall, 0, len(tx.Probes))
				for _, p := range tx.Probes {
					msg.ToolCalls = append(msg.ToolCalls, p.ToBabel())
				}
			}
			out = append(out, msg)

			for _, p := range tx.Probes {
				content := p.Summary(level(inc, tx, p))
				if p.Status == ProbePending || p.Status == ProbeRunning {
					content = "Tool execution interrupted; outcome unknown. Verify side effects before retrying."
				}
				out = append(out, babel.Message{
					Role:       "tool",
					Content:    content,
					ToolCallID: p.WireID,
				})
			}
		}
	}

	return out
}

// stateToLevel maps a residency State to the summary level it renders at. "" and
// Full both mean "show everything".
func stateToLevel(s State) SummaryLevel {
	switch s {
	case StateDigest:
		return SummaryCondensed
	case StateLine:
		return SummarySemantic
	case StateTombstone, StateElided:
		return SummaryTombstone
	default:
		return SummaryFull
	}
}

// AddTransmission creates a new Transmission on the incursion, converting raw
// babel ToolCall fragments into history Probe objects with parsed arguments.
// If a probe's arguments are not valid JSON, the Probe is still created and its
// ArgumentsError field is set; the caller can deny the probe later.
func (inc *Incursion) AddTransmission(text string, raw []babel.ToolCall) (*Transmission, error) {
	tx := &Transmission{
		Vinculum: Vinculum{
			ID:           newID("tx"),
			Kind:         "transmission",
			SessionID:    inc.SessionID,
			CollectiveID: inc.CollectiveID,
			PlanID:       inc.PlanID,
			ParentID:     inc.ID,
			Index:        len(inc.Transmissions),
			CreatedAt:    time.Now(),
		},
		Text:   text,
		Probes: make([]*Probe, 0, len(raw)),
	}
	if len(inc.Transmissions) > 0 {
		prev := inc.Transmissions[len(inc.Transmissions)-1]
		tx.LinkAfter(prev)
	}

	var firstErr error
	for _, tc := range raw {
		probe := &Probe{
			Vinculum: Vinculum{
				ID:           newID("probe"),
				Kind:         "probe",
				SessionID:    inc.SessionID,
				CollectiveID: inc.CollectiveID,
				PlanID:       inc.PlanID,
				ParentID:     tx.ID,
				Index:        len(tx.Probes),
				CreatedAt:    time.Now(),
			},
			WireID:       tc.ID,
			Name:         tc.Function.Name,
			ProbeType:    tc.Function.Name,
			ArgumentsRaw: tc.Function.Arguments,
			Status:       ProbePending,
		}
		if len(tx.Probes) > 0 {
			prev := tx.Probes[len(tx.Probes)-1]
			probe.LinkAfter(prev)
		}

		args := map[string]any{}
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				probe.ArgumentsError = err.Error()
			} else {
				probe.Extension = json.RawMessage(tc.Function.Arguments)
			}
		}
		probe.Arguments = args

		if firstErr == nil && probe.ArgumentsError != "" {
			firstErr = fmt.Errorf("probe %s: invalid arguments JSON", probe.ID)
		}

		probe.LinkTo(tx, "response_to")
		tx.LinkTo(probe, "spawned")
		tx.Probes = append(tx.Probes, probe)
	}

	// The first transmission is the incursion's reply: link it bidirectionally
	// with the human turn (the Incursion) so a response is linked to AND from
	// the event that prompted it. Later transmissions are tool-loop continuations.
	if len(inc.Transmissions) == 0 {
		tx.LinkTo(inc, "response_to")
		inc.LinkTo(tx, "spawned")
	}

	inc.Transmissions = append(inc.Transmissions, tx)
	return tx, firstErr
}

// FinalTransmission returns the last assistant transmission in the incursion,
// or nil if there are none.
func (inc *Incursion) FinalTransmission() *Transmission {
	if len(inc.Transmissions) == 0 {
		return nil
	}
	return inc.Transmissions[len(inc.Transmissions)-1]
}

// RebuildLinks reconstructs the hierarchical graph (incursions → transmissions
// → probes/synapses → patterns) from a flat slice of Entities. It is used by a
// future backend loader that fetches rows from a relational store. Links that
// live in Vinculum.Links are not rebuilt here; the backend populates them
// separately.
func (c *Collective) RebuildLinks(objs []Entity) {
	children := func(parentID, kind string) []Entity {
		var out []Entity
		for _, obj := range objs {
			v := obj.GetVinculum()
			if v.ParentID == parentID && v.Kind == kind {
				out = append(out, obj)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].GetVinculum().Index < out[j].GetVinculum().Index
		})
		return out
	}

	wire := func(list []Entity) {
		for i, e := range list {
			v := e.GetVinculum()
			if i > 0 {
				v.LeftID = list[i-1].ObjectID()
			}
			if i < len(list)-1 {
				v.RightID = list[i+1].ObjectID()
			}
		}
	}

	c.Incursions = nil
	incs := children(c.ID, "incursion")
	wire(incs)
	c.Incursions = make([]*Incursion, len(incs))
	for i, e := range incs {
		c.Incursions[i] = e.(*Incursion)
	}

	for _, inc := range c.Incursions {
		inc.Transmissions = nil
		txs := children(inc.ID, "transmission")
		wire(txs)
		inc.Transmissions = make([]*Transmission, len(txs))
		for i, e := range txs {
			inc.Transmissions[i] = e.(*Transmission)
		}

		for _, tx := range inc.Transmissions {
			tx.Probes = nil
			probes := children(tx.ID, "probe")
			wire(probes)
			tx.Probes = make([]*Probe, len(probes))
			for i, e := range probes {
				tx.Probes[i] = e.(*Probe)
			}

			tx.Synapses = nil
			synapses := children(tx.ID, "synapse")
			wire(synapses)
			tx.Synapses = make([]*Synapse, len(synapses))
			for i, e := range synapses {
				tx.Synapses[i] = e.(*Synapse)
			}
		}
	}

	for _, inc := range c.Incursions {
		for _, tx := range inc.Transmissions {
			for _, p := range tx.Probes {
				p.Patterns = nil
				patterns := children(p.ID, "pattern")
				wire(patterns)
				p.Patterns = make([]*Pattern, len(patterns))
				for i, e := range patterns {
					p.Patterns[i] = e.(*Pattern)
				}
			}
		}
	}
}
