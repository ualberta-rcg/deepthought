// Package graph is the server-side copy of the Borg-graph wire contract —
// the exact JSON shapes the CLI marshals when it pushes chats, duplicated
// here so the server depends on the CLI in NO way (no shared packages, no
// sibling modules). The duplication is deliberate: field names/types must
// stay byte-compatible with deepthought-cli/internal/history — change both
// together. Data + codec only; no stores, no drivers, no CLI imports.
package graph

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Vinculum is the base identity on every graph node. Untagged fields are the
// wire format — do not add tags.
type Vinculum struct {
	ID           string
	Kind         string
	SessionID    string
	CollectiveID string
	PlanID       string
	ParentID     string
	LeftID       string
	RightID      string
	Index        int
	Links        []Link
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Metadata     map[string]any
	Extra        json.RawMessage

	State  State
	Pinned bool

	Outcome     Outcome
	Producer    *Producer
	Cost        Cost
	Sensitivity Sensitivity
}

type Link struct {
	ID        string
	SourceID  string
	TargetID  string
	Relation  string
	Kind      string
	Metadata  map[string]any
	CreatedAt time.Time
}

// State controls which recoverable representation enters model context.
type State string

const (
	StateFull      State = "FULL"
	StateDigest    State = "DIGEST"
	StateLine      State = "LINE"
	StateTombstone State = "TOMBSTONE"
	StateElided    State = "ELIDED"
)

type Outcome string

const (
	OutcomeSuccess Outcome = "SUCCESS"
	OutcomeFailure Outcome = "FAILURE"
	OutcomePartial Outcome = "PARTIAL"
	OutcomeUnknown Outcome = "UNKNOWN"
)

type FailureClass string

type Sensitivity int

type Cost struct {
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	USD          float64 `json:"usd,omitempty"`
	WallclockMS  int64   `json:"wallclock_ms,omitempty"`
	GPUSeconds   float64 `json:"gpu_seconds,omitempty"`
}

type Producer struct {
	Provider       string         `json:"provider,omitempty"`
	ModelID        string         `json:"model_id,omitempty"`
	ModelVersion   string         `json:"model_version,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	Seed           *int64         `json:"seed,omitempty"`
	HarnessVersion string         `json:"harness_version,omitempty"`
}

// Decision mirrors queen.Decision (the permission verdict recorded on probes).
type Decision int

const (
	Allow Decision = iota
	Deny
	Ask
)

type SummaryLevel int

const (
	SummaryFull SummaryLevel = iota
	SummaryCondensed
	SummarySemantic
	SummaryTombstone
)

type SummarySet struct {
	Full      string
	Condensed string
	Semantic  string
	Tombstone string
}

type ResultView struct {
	Content   string
	IsError   bool
	Summary   string
	Summaries SummarySet
}

type ProbeStatus int

// String names the probe lifecycle state (matches the client's labels).
func (s ProbeStatus) String() string {
	switch s {
	case ProbePending:
		return "pending"
	case ProbeApproved:
		return "approved"
	case ProbeRunning:
		return "running"
	case ProbeCompleted:
		return "completed"
	case ProbeFailed:
		return "failed"
	}
	return fmt.Sprintf("ProbeStatus(%d)", int(s))
}

const (
	ProbePending ProbeStatus = iota
	ProbeApproved
	ProbeRunning
	ProbeCompleted
	ProbeFailed
)

type IncursionStatus int

const (
	IncursionPending IncursionStatus = iota
	IncursionStreaming
	IncursionDispatching
	IncursionCompleted
	IncursionFailed
	IncursionInterrupted
)

// Environment mirrors the CLI's captured execution environment (a nested
// field of Incursion — tags are part of the wire contract).
type Environment struct {
	Modules         []string          `json:"modules,omitempty"`
	ContainerDigest string            `json:"container_digest,omitempty"`
	Hardware        string            `json:"hardware,omitempty"`
	Driver          string            `json:"driver,omitempty"`
	Toolkit         string            `json:"toolkit,omitempty"`
	Seeds           map[string]string `json:"seeds,omitempty"`
	JobID           string            `json:"job_id,omitempty"`
	NodeList        string            `json:"node_list,omitempty"`
	HarnessVersion  string            `json:"harness_version,omitempty"`
	CapturedAt      time.Time         `json:"captured_at"`
}

// Pattern is a learned fact harvested from a probe result.
type Pattern struct {
	Vinculum
	Category       string
	Content        string
	SourceProbeID  string
	SourceToolName string
}

// Probe is one tool invocation inside a transmission.
type Probe struct {
	Vinculum
	WireID         string
	Name           string
	ProbeType      string
	ArgumentsRaw   string
	Arguments      map[string]any
	ArgumentsError string
	Decision       Decision
	DecisionReason string
	Status         ProbeStatus
	Result         ResultView
	Patterns       []*Pattern
	Summaries      SummarySet
	StartedAt      time.Time
	FinishedAt     time.Time
	Extension      json.RawMessage
}

// Transmission is one assistant message inside an incursion.
type Transmission struct {
	Vinculum
	Text     string
	Probes   []*Probe
	Synapses []*Synapse
}

// Synapse is a thinking/reasoning block attached to a transmission.
type Synapse struct {
	Vinculum
	Content string
	Type    string
}

// Incursion is one user turn.
type Incursion struct {
	Vinculum
	Status        IncursionStatus
	Prompt        string
	Transmissions []*Transmission
	Cycles        int
	Error         string
	Summaries     SummarySet
	CompletedAt   time.Time
	ObjectiveID   string
	Environment   Environment
	AttemptNumber int
}

// Collective is the whole chat transcript.
type Collective struct {
	Vinculum
	SystemPrompt string
	Title        string
	MaxCycles    int
	Incursions   []*Incursion
}

// RebuildLinks is a no-op on the server: link reconstruction is a client-side
// concern; the server persists and returns the stored graph as-is.
func (c *Collective) RebuildLinks(_ []Entity) {}

// Entity is the persistable-node interface (mirrors history.Entity).
type Entity interface {
	ObjectID() string
	ObjectKind() string
	GetVinculum() *Vinculum
}

func (v *Vinculum) ObjectID() string           { return v.ID }
func (c *Collective) ObjectKind() string       { return "collective" }
func (i *Incursion) ObjectKind() string        { return "incursion" }
func (t *Transmission) ObjectKind() string     { return "transmission" }
func (p *Probe) ObjectKind() string            { return "probe" }
func (p *Pattern) ObjectKind() string          { return "pattern" }
func (s *Synapse) ObjectKind() string          { return "synapse" }
func (c *Collective) GetVinculum() *Vinculum   { return &c.Vinculum }
func (i *Incursion) GetVinculum() *Vinculum    { return &i.Vinculum }
func (t *Transmission) GetVinculum() *Vinculum { return &t.Vinculum }
func (p *Probe) GetVinculum() *Vinculum        { return &p.Vinculum }
func (p *Pattern) GetVinculum() *Vinculum      { return &p.Vinculum }
func (s *Synapse) GetVinculum() *Vinculum      { return &s.Vinculum }

// ChatStore is the store contract both products' stores satisfy (the
// server's MySQL store is checked against it at compile time).
type ChatStore interface {
	Store
	Resume(id string) (*Collective, error)
	ListCollectives() ([]ChatSummary, error)
}

// Store is the base persistence contract.
type Store interface {
	CreateCollective(req SpawnCollectiveRequest) (*Collective, error)
	GetCollective(id string) (*Collective, error)
	SaveObject(obj Entity) error
	SaveCollective(coll *Collective) error
	Flush() error
}

// LegacyEntityDrone mirrors the client's legacyEntityDrone (see WrapEntity).
func LegacyEntityDrone(obj Entity) (*Drone, error) { return WrapEntity(obj) }

// ChatSummary is one row of the chat listing.
type ChatSummary struct {
	ID         string
	Title      string
	Model      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Incursions int
	Messages   int
}

// Drone satisfies Entity so stores can persist drones directly.
func (d *Drone) ObjectKind() string     { return d.Kind }
func (d *Drone) GetVinculum() *Vinculum { return &d.Vinculum }

// Record is one records-KV entry.
type Record struct {
	ID      string          `json:"id"`
	Data    json.RawMessage `json:"data"`
	Updated time.Time       `json:"updated"`
}

// Drone is the persisted interaction row (body + provenance columns).
type Drone struct {
	Vinculum
	Body        json.RawMessage
	BodyHash    []byte
	State       State
	Pinned      bool
	Poisoned    bool
	Tokens      map[State]int
	Cost        Cost
	Producer    Producer
	Outcome     Outcome
	FailClass   *FailureClass
	Sensitivity Sensitivity
	Embedding   []float32
	UseCount    int
	LastUsed    *time.Time
	Summaries   SummarySet
}

// InlineBodyLimit matches the client: bodies up to 1 MiB stay in-row.
const InlineBodyLimit = 1 << 20

// CanonicalBody canonicalizes (sorted-key re-marshal) and hashes a body —
// the content-addressing anchor, identical to the client's.
func CanonicalBody(body any) (json.RawMessage, []byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("graph: marshal body: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, nil, fmt.Errorf("graph: normalize body: %w", err)
	}
	raw, err = json.Marshal(normalized)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(raw)
	return raw, sum[:], nil
}

// NewID mirrors the client's ID scheme (prefix + random hex).
func NewID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

// StartIncursion appends a new pending incursion and returns it (test/graph
// convenience; the server never synthesizes user turns itself).
func (c *Collective) StartIncursion(prompt string) *Incursion {
	inc := &Incursion{
		Vinculum: Vinculum{
			ID:           NewID("inc"),
			Kind:         "incursion",
			SessionID:    c.SessionID,
			CollectiveID: c.ID,
			ParentID:     c.ID,
			CreatedAt:    time.Now(),
		},
		Prompt: prompt,
	}
	c.Incursions = append(c.Incursions, inc)
	return inc
}

// SpawnCollectiveRequest carries the IDs for a new Collective.
type SpawnCollectiveRequest struct {
	SystemPrompt string
	PlanID       string
	SessionID    string
}

// NewCollective creates an empty collective (server-side tests).
func NewCollective(req SpawnCollectiveRequest) *Collective {
	return &Collective{
		Vinculum: Vinculum{
			ID:        NewID("coll"),
			Kind:      "collective",
			SessionID: req.SessionID,
			PlanID:    req.PlanID,
			CreatedAt: time.Now(),
		},
		SystemPrompt: req.SystemPrompt,
		MaxCycles:    25,
	}
}

// DecodeByKind rehydrates an entity from its marshaled JSON (the six
// chat-graph kinds the client pushes).
func DecodeByKind(kind string, raw []byte) (any, error) {
	switch kind {
	case "collective":
		var c Collective
		return &c, json.Unmarshal(raw, &c)
	case "incursion":
		var i Incursion
		return &i, json.Unmarshal(raw, &i)
	case "transmission":
		var t Transmission
		return &t, json.Unmarshal(raw, &t)
	case "probe":
		var p Probe
		return &p, json.Unmarshal(raw, &p)
	case "pattern":
		var p Pattern
		return &p, json.Unmarshal(raw, &p)
	case "synapse":
		var s Synapse
		return &s, json.Unmarshal(raw, &s)
	}
	return nil, fmt.Errorf("graph: unknown kind %q", kind)
}

// LoadEntitiesWithPatterns flattens a collective into its persistable
// entities (all nodes, patterns included).
func LoadEntitiesWithPatterns(coll *Collective) []Entity {
	out := LoadEntities(coll)
	for _, inc := range coll.Incursions {
		for _, tx := range inc.Transmissions {
			for _, probe := range tx.Probes {
				for _, pattern := range probe.Patterns {
					out = append(out, pattern)
				}
			}
		}
	}
	return out
}

// LoadEntities flattens the collective tree (no patterns).
func LoadEntities(coll *Collective) []Entity {
	out := []Entity{coll}
	for _, inc := range coll.Incursions {
		out = append(out, inc)
		for _, tx := range inc.Transmissions {
			out = append(out, tx)
			for _, probe := range tx.Probes {
				out = append(out, probe)
			}
			for _, syn := range tx.Synapses {
				out = append(out, syn)
			}
		}
	}
	return out
}

// WrapEntity mirrors the client's legacyEntityDrone: it derives the Drone
// columns (state, outcome, provenance) from a graph node.
func WrapEntity(obj Entity) (*Drone, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	drone := &Drone{Vinculum: Vinculum{ID: NewUUIDv7(), Kind: obj.ObjectKind(), SessionID: obj.GetVinculum().SessionID, CreatedAt: time.Now()}}
	drone.Vinculum = *obj.GetVinculum()
	v := obj.GetVinculum()
	drone.State = v.State
	if drone.State == "" {
		drone.State = StateFull
	}
	drone.Pinned = v.Pinned
	drone.Outcome = outcomeFromEntity(obj)
	if v.Producer != nil {
		drone.Producer = *v.Producer
	}
	drone.Cost = v.Cost
	drone.Sensitivity = v.Sensitivity
	drone.Body = raw
	return drone, nil
}

func outcomeFromEntity(obj Entity) Outcome {
	switch e := obj.(type) {
	case *Probe:
		switch e.Status {
		case ProbeCompleted:
			return OutcomeSuccess
		case ProbeFailed:
			return OutcomeFailure
		}
	case *Incursion:
		switch e.Status {
		case IncursionCompleted:
			return OutcomeSuccess
		case IncursionFailed:
			return OutcomeFailure
		case IncursionInterrupted:
			return OutcomePartial
		}
	}
	return obj.GetVinculum().Outcome
}

// NewUUIDv7 mirrors the client's UUID scheme.
func NewUUIDv7() string {
	var id [16]byte
	ms := uint64(time.Now().UnixMilli())
	for i := 5; i >= 0; i-- {
		id[i] = byte(ms)
		ms >>= 8
	}
	_, _ = rand.Read(id[6:])
	id[6] = (id[6] & 0x0f) | 0x70
	id[8] = (id[8] & 0x3f) | 0x80
	return hex.EncodeToString(id[:])
}
