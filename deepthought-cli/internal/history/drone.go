package history

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// State controls which recoverable representation enters model context.
type State string

const (
	StateFull      State = "FULL"
	StateDigest    State = "DIGEST"
	StateLine      State = "LINE"
	StateTombstone State = "TOMBSTONE"
	StateElided    State = "ELIDED"
)

var stateOrder = []State{StateFull, StateDigest, StateLine, StateTombstone, StateElided}

type Outcome string

const (
	OutcomeSuccess Outcome = "SUCCESS"
	OutcomeFailure Outcome = "FAILURE"
	OutcomePartial Outcome = "PARTIAL"
	OutcomeUnknown Outcome = "UNKNOWN"
)

type FailureClass string

const (
	FailureTransient  FailureClass = "transient"
	FailureResource   FailureClass = "resource"
	FailureUser       FailureClass = "user"
	FailureScientific FailureClass = "scientific"
)

// Sensitivity is a total order. Derived content inherits the highest source.
type Sensitivity int

const (
	SensitivityPublic Sensitivity = iota
	SensitivityInternal
	SensitivityRestricted
	SensitivitySecret
)

func (s Sensitivity) String() string {
	switch s {
	case SensitivityPublic:
		return "public"
	case SensitivityInternal:
		return "internal"
	case SensitivityRestricted:
		return "restricted"
	case SensitivitySecret:
		return "secret"
	default:
		return "unknown"
	}
}

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

// Drone is the single persisted interaction record. Body is typed by Kind via
// Registry; all kinds share one table and one slice without generics.
type Drone struct {
	Vinculum
	Body        json.RawMessage `json:"body"`
	BodyHash    []byte          `json:"body_hash"`
	State       State           `json:"state"`
	Pinned      bool            `json:"pinned"`
	Poisoned    bool            `json:"poisoned"`
	Tokens      map[State]int   `json:"tokens,omitempty"`
	Cost        Cost            `json:"cost"`
	Producer    Producer        `json:"producer"`
	Outcome     Outcome         `json:"outcome"`
	FailClass   *FailureClass   `json:"fail_class,omitempty"`
	Sensitivity Sensitivity     `json:"sensitivity"`
	Embedding   []float32       `json:"embedding,omitempty"`
	UseCount    int             `json:"use_count"`
	LastUsed    *time.Time      `json:"last_used,omitempty"`
	Summaries   SummarySet      `json:"summaries"`
}

func NewDrone(kind, sessionID string, body any) (*Drone, error) {
	raw, hash, err := canonicalBody(body)
	if err != nil {
		return nil, err
	}
	return &Drone{
		Vinculum: Vinculum{ID: newUUIDv7(), Kind: kind, SessionID: sessionID, CreatedAt: time.Now()},
		Body:     raw, BodyHash: hash, State: StateFull, Outcome: OutcomeUnknown,
		Tokens: map[State]int{},
	}, nil
}

func canonicalBody(body any) (json.RawMessage, []byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("history: marshal body: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, nil, fmt.Errorf("history: normalize body: %w", err)
	}
	raw, err = json.Marshal(normalized)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(raw)
	return raw, sum[:], nil
}

// Demote changes only residency. Bodies are immutable and never destroyed.
func (d *Drone) Demote(to State) error {
	if d.Kind == "annotation" {
		return errors.New("history: annotations may not be demoted")
	}
	if d.Pinned {
		return errors.New("history: pinned drone may not be demoted")
	}
	from := stateIndex(d.State)
	next := stateIndex(to)
	if next < from {
		return errors.New("history: demotion cannot promote state")
	}
	d.State = to
	d.Touch()
	return nil
}

func stateIndex(state State) int {
	for i, candidate := range stateOrder {
		if candidate == state {
			return i
		}
	}
	return 0
}

// InheritSensitivity enforces transitive derived_from sensitivity.
func (d *Drone) InheritSensitivity(sources ...*Drone) {
	for _, source := range sources {
		if source != nil && source.Sensitivity > d.Sensitivity {
			d.Sensitivity = source.Sensitivity
		}
	}
}

// EligibleFor prevents fallback from relaxing a label.
func (d *Drone) EligibleFor(clearance Sensitivity) bool {
	return clearance >= d.Sensitivity
}

type Handler interface {
	Render(context.Context, *Drone, State) (string, error)
	Summarize(context.Context, *Drone, State) (string, error)
	Validate(*Drone) error
	Embed(context.Context, *Drone) ([]float32, error)
}

type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: map[string]Handler{}}
}

func (r *HandlerRegistry) Register(kind string, handler Handler) error {
	if kind == "" || handler == nil {
		return errors.New("history: kind and handler are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[kind]; exists {
		return fmt.Errorf("history: handler %q already registered", kind)
	}
	r.handlers[kind] = handler
	return nil
}

func (r *HandlerRegistry) Handler(kind string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.handlers[kind]
	return handler, ok
}

type AnnotationBody struct {
	TargetID string `json:"target_id"`
	Author   string `json:"author"`
	Text     string `json:"text"`
}

func NewAnnotation(sessionID, targetID, author, text string) (*Drone, error) {
	drone, err := NewDrone("annotation", sessionID, AnnotationBody{
		TargetID: targetID, Author: author, Text: text,
	})
	if err != nil {
		return nil, err
	}
	drone.Pinned = true
	drone.Links = append(drone.Links, Link{
		ID: newID("link"), SourceID: drone.ID, TargetID: targetID,
		Relation: "annotates", Kind: "drone", CreatedAt: time.Now(),
	})
	return drone, nil
}

// newUUIDv7 emits a standards-shaped, time-ordered UUIDv7 without a dependency.
func newUUIDv7() string {
	var id [16]byte
	ms := uint64(time.Now().UnixMilli())
	for i := 5; i >= 0; i-- {
		id[i] = byte(ms)
		ms >>= 8
	}
	_, _ = rand.Read(id[6:])
	id[6] = (id[6] & 0x0f) | 0x70
	id[8] = (id[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(id[:])
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}
