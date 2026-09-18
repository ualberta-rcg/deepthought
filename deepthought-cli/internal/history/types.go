// Package history owns Annorax's conversation object model: Collectives,
// Incursions, Transmissions, Probes, Patterns, and Synapses. It sits between
// the TUI and the Babel wire adapter: the TUI builds rich objects, and Babel
// receives only the flattened []Message produced by Collective.Messages.
//
// The package is designed to become backend-persistent later. Store is an
// interface (MemStore/FileStore/SQLiteStore implement it) that an API client
// can satisfy; SummaryEngine is a struct of built-in summary functions.
package history

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"annorax/internal/babel"
	"annorax/internal/queen"
)

// SummaryLevel selects how much of an object's content to put into context.
type SummaryLevel int

const (
	SummaryFull SummaryLevel = iota
	SummaryCondensed
	SummarySemantic
	SummaryTombstone
)

func (l SummaryLevel) String() string {
	switch l {
	case SummaryFull:
		return "full"
	case SummaryCondensed:
		return "condensed"
	case SummarySemantic:
		return "semantic"
	case SummaryTombstone:
		return "tombstone"
	default:
		return fmt.Sprintf("SummaryLevel(%d)", l)
	}
}

// SummarySet holds progressively lossy views of the same content. When a level
// is requested but empty, serialization falls back toward the next richer level.
// None of these are generated automatically during chat; external systems call
// the SummaryEngine to populate them.
type SummarySet struct {
	Full      string // original, complete text
	Condensed string // line/char-truncated preview
	Semantic  string // LLM-generated summary
	Tombstone string // minimal marker, e.g. "[summarized]"
}

// At returns the requested summary, falling back to richer levels if it is empty.
// Tombstone is always returned as a last resort.
func (s SummarySet) At(level SummaryLevel) string {
	switch level {
	case SummaryTombstone:
		if s.Tombstone != "" {
			return s.Tombstone
		}
		fallthrough
	case SummarySemantic:
		if s.Semantic != "" {
			return s.Semantic
		}
		fallthrough
	case SummaryCondensed:
		if s.Condensed != "" {
			return s.Condensed
		}
		fallthrough
	default:
		return s.Full
	}
}

// Pattern is a durable fact extracted from a probe result.
type Pattern struct {
	Vinculum              // Kind == "pattern"
	Category       string // e.g. "env", "file", "constraint"
	Content        string
	SourceProbeID  string
	SourceToolName string
}

// ResultView is the model/TUI-facing result of one probe.
type ResultView struct {
	Content   string     // full model-facing payload
	IsError   bool       // success/failure outcome
	Summary   string     // one-line TUI chrome
	Summaries SummarySet // progressively lossy views of Content
}

// ProbeStatus tracks execution state for UI/backend observability.
type ProbeStatus int

const (
	ProbePending ProbeStatus = iota
	ProbeAllowed
	ProbeRunning
	ProbeCompleted
	ProbeDenied
	ProbeFailed
)

func (s ProbeStatus) String() string {
	switch s {
	case ProbePending:
		return "pending"
	case ProbeAllowed:
		return "allowed"
	case ProbeRunning:
		return "running"
	case ProbeCompleted:
		return "completed"
	case ProbeDenied:
		return "denied"
	case ProbeFailed:
		return "failed"
	default:
		return fmt.Sprintf("ProbeStatus(%d)", s)
	}
}

// Probe is one tool invocation inside a transmission.
type Probe struct {
	Vinculum              // Kind == "probe"
	WireID         string // model-assigned ID used in babel pairing
	Name           string // wire tool name, e.g. "read"
	ProbeType      string // discriminator: "read", "bash", or "function" for unknown
	ArgumentsRaw   string
	Arguments      map[string]any
	ArgumentsError string
	Decision       queen.Decision
	DecisionReason string
	Status         ProbeStatus
	Result         ResultView
	Patterns       []*Pattern
	Summaries      SummarySet
	StartedAt      time.Time
	FinishedAt     time.Time
	Extension      json.RawMessage // typed tool-use payload (JSONB in the DB)
}

// ToBabel converts a Probe back to the wire ToolCall shape.
func (p *Probe) ToBabel() babel.ToolCall {
	return babel.ToolCall{
		ID:   p.WireID,
		Type: "function",
		Function: babel.FunctionCall{
			Name:      p.Name,
			Arguments: p.ArgumentsRaw,
		},
	}
}

// Summary returns the result content at the requested summary level.
func (p *Probe) Summary(level SummaryLevel) string {
	return p.Result.Summaries.At(level)
}

// Transmission is one assistant message inside an incursion. An incursion may
// contain several transmissions when the model tool-loops.
type Transmission struct {
	Vinculum // Kind == "transmission"
	Text     string
	Probes   []*Probe
	Synapses []*Synapse // thinking/reasoning blocks emitted by the AI
}

// AddSynapse appends a thinking block to the transmission.
func (t *Transmission) AddSynapse(content, synapseType string) *Synapse {
	s := &Synapse{
		Vinculum: Vinculum{
			ID:           newID("synapse"),
			Kind:         "synapse",
			SessionID:    t.SessionID,
			CollectiveID: t.CollectiveID,
			PlanID:       t.PlanID,
			ParentID:     t.ID,
			Index:        len(t.Synapses),
			CreatedAt:    time.Now(),
		},
		Content: content,
		Type:    synapseType,
	}
	if len(t.Synapses) > 0 {
		prev := t.Synapses[len(t.Synapses)-1]
		s.LinkAfter(prev)
	}
	t.Synapses = append(t.Synapses, s)
	return s
}

// Synapse is a thinking/reasoning block emitted by the AI. It is attached to a
// transmission (and optionally linked to probes or other objects via Links).
type Synapse struct {
	Vinculum // Kind == "synapse"
	Content  string
	Type     string // e.g. "reasoning", "planning", "reflection"
}

// IncursionStatus tracks where an incursion is in its lifecycle.
type IncursionStatus int

const (
	IncursionPending IncursionStatus = iota
	IncursionStreaming
	IncursionDispatching
	IncursionCompleted
	IncursionFailed
	IncursionInterrupted
)

func (s IncursionStatus) String() string {
	switch s {
	case IncursionPending:
		return "pending"
	case IncursionStreaming:
		return "streaming"
	case IncursionDispatching:
		return "dispatching"
	case IncursionCompleted:
		return "completed"
	case IncursionFailed:
		return "failed"
	case IncursionInterrupted:
		return "interrupted"
	default:
		return fmt.Sprintf("IncursionStatus(%d)", s)
	}
}

// Incursion groups one user prompt with all assistant transmissions and probes
// that resolve it.
type Incursion struct {
	Vinculum      // Kind == "incursion"
	Status        IncursionStatus
	Prompt        string
	Transmissions []*Transmission
	Cycles        int // tool-loop counter
	Error         string
	Summaries     SummarySet
	CompletedAt   time.Time
	ObjectiveID   string
	Environment   Environment
	AttemptNumber int
}

// FindProbe returns the probe with the given WireID inside this incursion.
func (i *Incursion) FindProbe(wireID string) *Probe {
	for _, tx := range i.Transmissions {
		for _, p := range tx.Probes {
			if p.WireID == wireID {
				return p
			}
		}
	}
	return nil
}

// MarkCompleted finalizes a successful incursion.
func (i *Incursion) MarkCompleted() {
	i.Status = IncursionCompleted
	i.CompletedAt = time.Now()
	i.Touch()
}

// Fail finalizes a failed incursion.
func (i *Incursion) Fail(reason string) {
	i.Status = IncursionFailed
	i.Error = reason
	i.CompletedAt = time.Now()
	i.Touch()
}

// Interrupt preserves partial work while pausing the turn for a later resume.
func (i *Incursion) Interrupt(reason string) {
	i.Status = IncursionInterrupted
	i.Error = reason
	i.Touch()
}

// SpawnCollectiveRequest carries the IDs and settings needed to create a new
// Collective.
type SpawnCollectiveRequest struct {
	SystemPrompt string
	PlanID       string
	SessionID    string
}

// Collective is the in-memory session transcript.
type Collective struct {
	Vinculum     // Kind == "collective"
	SystemPrompt string
	Title        string // short human-friendly name (summary-model generated)
	MaxCycles    int
	Cycles       int // total cycles across all incursions
	Incursions   []*Incursion
}

// NewCollective builds an empty collective.
func NewCollective(req SpawnCollectiveRequest) *Collective {
	return &Collective{
		Vinculum: Vinculum{
			ID:        newID("coll"),
			Kind:      "collective",
			SessionID: req.SessionID,
			PlanID:    req.PlanID,
			CreatedAt: time.Now(),
		},
		SystemPrompt: req.SystemPrompt,
		MaxCycles:    25,
		Incursions:   nil,
	}
}

// StartIncursion appends a new pending incursion and returns it.
func (c *Collective) StartIncursion(prompt string) *Incursion {
	inc := &Incursion{
		Vinculum: Vinculum{
			ID:           newID("inc"),
			Kind:         "incursion",
			SessionID:    c.SessionID,
			CollectiveID: c.ID,
			PlanID:       c.PlanID,
			ParentID:     c.ID,
			Index:        len(c.Incursions),
			CreatedAt:    time.Now(),
		},
		Status: IncursionPending,
		Prompt: prompt,
	}
	if len(c.Incursions) > 0 {
		prev := c.Incursions[len(c.Incursions)-1]
		inc.LinkAfter(prev)
	}
	c.Incursions = append(c.Incursions, inc)
	return inc
}

// ActiveIncursion returns the latest incursion that is not yet completed or failed.
func (c *Collective) ActiveIncursion() *Incursion {
	for i := len(c.Incursions) - 1; i >= 0; i-- {
		switch c.Incursions[i].Status {
		case IncursionPending, IncursionStreaming, IncursionDispatching:
			return c.Incursions[i]
		}
	}
	return nil
}

// LastInterrupted returns the newest resumable turn.
func (c *Collective) LastInterrupted() *Incursion {
	for i := len(c.Incursions) - 1; i >= 0; i-- {
		if c.Incursions[i].Status == IncursionInterrupted {
			return c.Incursions[i]
		}
	}
	return nil
}

// newID generates a short random ID with the given prefix.
func newID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a timestamp-based ID if crypto/rand fails.
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}
