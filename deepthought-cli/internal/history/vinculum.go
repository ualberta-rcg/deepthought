package history

import (
	"encoding/json"
	"time"
)

// Vinculum is the linking/RDB metadata present on every persisted object.
// In Borg canon the vinculum is the device that links each drone to the
// Collective, filters individual thought, and harmonizes the whole. Here it
// is the base record every history object embeds: IDs, links, age, and
// open-ended metadata/extra blobs.
type Vinculum struct {
	ID           string // primary key, e.g. probe_abc123
	Kind         string // "collective", "incursion", "transmission", "probe", "pattern", "synapse"
	SessionID    string // SSH session / process instance
	CollectiveID string // FK to owning collective
	PlanID       string // optional FK to a future plan object
	ParentID     string // immediate container
	LeftID       string // previous sibling (empty if first)
	RightID      string // next sibling (empty if last)
	Index        int    // stable ordering within parent
	Links        []Link // arbitrary cross-object graph links
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Metadata     map[string]any  // extensible key/value for future well-known fields
	Extra        json.RawMessage // catch-all for unthought-of fields

	// Residency state for context assembly. "" means Full (the default). A
	// future Queen AI thread demotes objects (Full→Digest→…→Tombstone) via
	// Drone.Demote; GetCollective rehydrates this from the Drone's state column
	// so MessagesByState renders each node at its own residency.
	State  State
	Pinned bool

	// Provenance/metadata mirrored onto the persisted Drone (Drone's own fields
	// shadow these when both exist). Outcome is derived from the node's lifecycle
	// status at save time; Producer/Cost are set by the chat at commit; default
	// zero otherwise. GetCollective rehydrates State/Outcome/Sensitivity.
	Outcome     Outcome
	Producer    *Producer
	Cost        Cost
	Sensitivity Sensitivity
}

// Entity is anything that can be persisted and linked in the history graph.
type Entity interface {
	ObjectID() string
	ObjectKind() string
	GetVinculum() *Vinculum
}

// ObjectID returns the object's primary key. It satisfies Entity.
func (v *Vinculum) ObjectID() string { return v.ID }

// ObjectKind returns the object's kind discriminator. It satisfies Entity.
func (v *Vinculum) ObjectKind() string { return v.Kind }

// GetVinculum returns the base record. It satisfies Entity.
func (v *Vinculum) GetVinculum() *Vinculum { return v }

// Age returns how long ago the object was created.
func (v *Vinculum) Age() time.Duration { return time.Since(v.CreatedAt) }

// AgeAt returns how long before t the object was created.
func (v *Vinculum) AgeAt(t time.Time) time.Duration { return t.Sub(v.CreatedAt) }

// Touch updates the mechanical UpdatedAt timestamp.
func (v *Vinculum) Touch() { v.UpdatedAt = time.Now() }

// LinkTo creates a generic edge from this object to target and appends it to
// this object's Links slice. It does not add a reciprocal link automatically.
func (v *Vinculum) LinkTo(target Entity, relation string) *Link {
	link := Link{
		ID:        newID("link"),
		SourceID:  v.ID,
		TargetID:  target.ObjectID(),
		Relation:  relation,
		Kind:      target.ObjectKind(),
		CreatedAt: time.Now(),
	}
	v.Links = append(v.Links, link)
	return &v.Links[len(v.Links)-1]
}

// SetLeft wires the previous-sibling pointer. Callers must also update the
// sibling's RightID for a doubly-linked list.
func (v *Vinculum) SetLeft(left Entity) {
	if left == nil {
		v.LeftID = ""
		return
	}
	v.LeftID = left.ObjectID()
}

// SetRight wires the next-sibling pointer. Callers must also update the
// sibling's LeftID for a doubly-linked list.
func (v *Vinculum) SetRight(right Entity) {
	if right == nil {
		v.RightID = ""
		return
	}
	v.RightID = right.ObjectID()
}

// LinkAfter splices this object after prev, updating both objects' pointers.
// If prev is nil, only RightID is cleared (the object becomes the first node).
func (v *Vinculum) LinkAfter(prev Entity) {
	if prev == nil {
		v.LeftID = ""
		return
	}
	prevV := prev.GetVinculum()
	v.LeftID = prevV.ID
	prevV.RightID = v.ID
}

// LinkBefore splices this object before next, updating both objects' pointers.
// If next is nil, only LeftID is cleared (the object becomes the last node).
func (v *Vinculum) LinkBefore(next Entity) {
	if next == nil {
		v.RightID = ""
		return
	}
	nextV := next.GetVinculum()
	v.RightID = nextV.ID
	nextV.LeftID = v.ID
}

// Link is a first-class edge between two history objects. Links live on the
// source object's Vinculum and can be serialized as separate rows or a JSONB
// column in a future backend.
type Link struct {
	ID        string
	SourceID  string
	TargetID  string
	Relation  string // e.g. "response_to", "result_of", "thought_for", "spawned"
	Kind      string // target kind, denormalized for reconstruction
	Metadata  map[string]any
	CreatedAt time.Time
}
