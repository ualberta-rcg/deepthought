package history

import (
	"fmt"
	"sync"
)

// Store persists and retrieves the history graph. The in-memory implementation
// is used now; a backend API client implements it later.
type Store interface {
	CreateCollective(req SpawnCollectiveRequest) (*Collective, error)
	GetCollective(id string) (*Collective, error)
	SaveObject(obj Entity) error
	SaveCollective(coll *Collective) error
	Flush() error // batch write hook
}

// ChatStore is a Store that can also enumerate and resume chats — everything
// the chat layer (NewChatModel/ResumeChatModel) and the Continue screen need.
// Both FileStore and SQLiteStore implement it; the chat programs against this
// interface so the live store can change without touching call sites. MemStore
// does not (it has no listing/resume semantics).
type ChatStore interface {
	Store
	Resume(id string) (*Collective, error)
	ListCollectives() ([]ChatSummary, error)
}

// ChatStoreSource returns a ChatStore for one chat session. FileStore is
// single-active (one collective per instance), so it needs a FRESH instance per
// chat; SQLiteStore is stateless and the source returns the same shared
// singleton each call. Callers that build a chat (NewChatModel/ResumeChatModel)
// and the Continue lister take a source, not a single store.
type ChatStoreSource func() ChatStore

// UsageReporter aggregates token usage across history. SQLiteStore implements
// it; the Status dashboard uses it for the per-model token view. Optional so
// non-SQLite stores (and tests) aren't forced to provide it.
type UsageReporter interface {
	UsageByModel() (map[string]Cost, error)
}

// MemStore is an in-memory Store. It is safe for concurrent use within a single
// session. It does not persist across process restarts.
type MemStore struct {
	mu         sync.RWMutex
	byID       map[string]Entity
	collective *Collective
	dirty      []*Vinculum // queue of objects touched since last Flush
}

// NewMemStore builds a new empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		byID:  make(map[string]Entity),
		dirty: make([]*Vinculum, 0),
	}
}

// CreateCollective stores the collective and returns it.
func (s *MemStore) CreateCollective(req SpawnCollectiveRequest) (*Collective, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.collective != nil {
		return nil, fmt.Errorf("memstore: collective already exists")
	}
	coll := NewCollective(req)
	s.collective = coll
	s.byID[coll.ID] = coll
	s.markDirty(&coll.Vinculum)
	return coll, nil
}

// GetCollective returns the stored collective.
func (s *MemStore) GetCollective(id string) (*Collective, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.collective == nil || s.collective.ID != id {
		return nil, fmt.Errorf("memstore: collective %q not found", id)
	}
	return s.collective, nil
}

// SaveObject stores one entity in the graph and queues it for the next batch
// flush. It updates the dirty queue even though the object is already in memory,
// so a future API client can send deltas.
func (s *MemStore) SaveObject(obj Entity) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if obj == nil {
		return fmt.Errorf("memstore: cannot save nil object")
	}
	v := obj.GetVinculum()
	if v.ID == "" {
		return fmt.Errorf("memstore: object has no ID")
	}
	s.byID[v.ID] = obj
	s.markDirty(v)
	return nil
}

// SaveCollective walks the entire graph and calls SaveObject on every node.
func (s *MemStore) SaveCollective(coll *Collective) error {
	if coll == nil {
		return fmt.Errorf("memstore: cannot save nil collective")
	}
	if err := s.SaveObject(coll); err != nil {
		return err
	}
	for _, inc := range coll.Incursions {
		if err := s.SaveObject(inc); err != nil {
			return err
		}
		for _, tx := range inc.Transmissions {
			if err := s.SaveObject(tx); err != nil {
				return err
			}
			for _, p := range tx.Probes {
				if err := s.SaveObject(p); err != nil {
					return err
				}
				for _, pattern := range p.Patterns {
					if err := s.SaveObject(pattern); err != nil {
						return err
					}
				}
			}
			for _, syn := range tx.Synapses {
				if err := s.SaveObject(syn); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Flush is a no-op for the in-memory store, but it returns the dirty queue so a
// future API client can batch-write the touched records. After Flush the dirty
// queue is reset.
func (s *MemStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// In-memory implementation: nothing to send. A backend store would send
	// s.dirty to the API here.
	_ = s.dirty
	s.dirty = s.dirty[:0]
	return nil
}

// Dirty returns a snapshot of records touched since the last Flush. The in-
// memory store uses this only for testing/monitoring; a backend store uses it
// to batch writes.
func (s *MemStore) Dirty() []*Vinculum {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Vinculum, len(s.dirty))
	copy(out, s.dirty)
	return out
}

func (s *MemStore) markDirty(v *Vinculum) {
	v.Touch()
	s.dirty = append(s.dirty, v)
}

// SaveIncursion is a convenience wrapper around SaveObject.
func SaveIncursion(store Store, inc *Incursion) error { return store.SaveObject(inc) }

// SaveTransmission is a convenience wrapper around SaveObject.
func SaveTransmission(store Store, tx *Transmission) error { return store.SaveObject(tx) }

// SaveProbe is a convenience wrapper around SaveObject.
func SaveProbe(store Store, p *Probe) error { return store.SaveObject(p) }

// SavePattern is a convenience wrapper around SaveObject.
func SavePattern(store Store, pattern *Pattern) error { return store.SaveObject(pattern) }

// SaveSynapse is a convenience wrapper around SaveObject.
func SaveSynapse(store Store, syn *Synapse) error { return store.SaveObject(syn) }
