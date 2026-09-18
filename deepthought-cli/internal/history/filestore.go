package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileStore persists the history graph as one JSONL file per collective
// (Claude Code style): every SaveObject appends one line tagged by the
// object's Kind, so a crash mid-turn loses at most the in-flight object. On
// load, lines decode by Kind into the right concrete type and RebuildLinks
// reweaves the graph; a later write of the same ID shadows earlier ones
// (last-wins), which keeps the file from needing compaction to be correct.
//
// Files live under dir as <collectiveID>.jsonl. The file is created lazily:
// CreateCollective only stages the collective in memory — nothing hits disk
// until the first SaveObject, so launching the app never litters the Continue
// list with empty chats.
type FileStore struct {
	mu      sync.Mutex
	dir     string
	id      string // active collective ID (empty until CreateCollective)
	path    string // active file path
	byID    map[string]Entity
	created bool // the collective line has been written to disk
}

// NewFileStore builds a store rooted at dir. The directory is created lazily.
func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir, byID: make(map[string]Entity)}
}

// CreateCollective stages a new collective in memory (no disk write yet). The
// file is created on the first SaveObject.
func (s *FileStore) CreateCollective(req SpawnCollectiveRequest) (*Collective, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.id != "" {
		return nil, fmt.Errorf("filestore: collective %q already active", s.id)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("filestore: mkdir: %w", err)
	}
	coll := NewCollective(req)
	s.id = coll.ID
	s.path = filepath.Join(s.dir, coll.ID+".jsonl")
	s.byID[coll.ID] = coll
	return coll, nil
}

// GetCollective reads <dir>/<id>.jsonl, rebuilding the full graph. It does
// NOT make the collective active for writes — use Resume for that.
func (s *FileStore) GetCollective(id string) (*Collective, error) {
	objs, err := loadObjects(filepath.Join(s.dir, id+".jsonl"))
	if err != nil {
		return nil, err
	}
	var coll *Collective
	for _, o := range objs {
		if c, ok := o.(*Collective); ok {
			coll = c
		}
	}
	if coll == nil {
		return nil, fmt.Errorf("filestore: collective %q not found", id)
	}
	coll.RebuildLinks(flattenTo(objs))
	return coll, nil
}

// Resume loads a collective AND marks it active so subsequent SaveObject calls
// append to its file. Used by the Continue screen to pick up an old chat.
func (s *FileStore) Resume(id string) (*Collective, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id != "" {
		return nil, fmt.Errorf("filestore: collective %q already active", s.id)
	}
	coll, err := s.GetCollective(id)
	if err != nil {
		return nil, err
	}
	s.id = coll.ID
	s.path = filepath.Join(s.dir, coll.ID+".jsonl")
	s.created = true // the file already exists; future appends go straight in
	for _, o := range loadEntities(coll) {
		s.byID[o.ObjectID()] = o
	}
	return coll, nil
}

// loadEntities flattens the graph back into the entity map for the active store.
func loadEntities(coll *Collective) []Entity {
	var out []Entity
	out = append(out, coll)
	for _, inc := range coll.Incursions {
		out = append(out, inc)
		for _, tx := range inc.Transmissions {
			out = append(out, tx)
			for _, p := range tx.Probes {
				out = append(out, p)
			}
			for _, s := range tx.Synapses {
				out = append(out, s)
			}
		}
	}
	return out
}

// SaveObject appends one entity as a JSONL line. Safe for concurrent calls
// within a session (the chat loop is single-threaded per session, but the
// mutex guards the shared file).
func (s *FileStore) SaveObject(obj Entity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if obj == nil || obj.GetVinculum().ID == "" {
		return fmt.Errorf("filestore: cannot save empty object")
	}
	s.byID[obj.ObjectID()] = obj
	return s.appendLocked(obj)
}

// SaveCollective walks the graph and appends every node. Used by callers that
// want a full snapshot; the chat loop calls SaveObject incrementally instead.
func (s *FileStore) SaveCollective(coll *Collective) error {
	if coll == nil {
		return fmt.Errorf("filestore: cannot save nil collective")
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

// Flush is a no-op for FileStore — writes are appended on SaveObject. Kept to
// satisfy the Store interface; a future backend would fsync here.
func (s *FileStore) Flush() error { return nil }

// appendLocked marshals obj and appends it as one line to the active file. On
// the first write to a fresh collective, the collective line is written first
// (CreateCollective deferred it) so a resume always finds a collective root.
func (s *FileStore) appendLocked(obj Entity) error {
	if !s.created {
		if coll, ok := s.byID[s.id].(*Collective); ok {
			if err := writeLine(s.path, coll); err != nil {
				return err
			}
		}
		s.created = true
	}
	return writeLine(s.path, obj)
}

// writeLine marshals one entity and appends it as a line.
func writeLine(path string, obj Entity) error {
	raw, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("filestore: marshal %s: %w", obj.ObjectKind(), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("filestore: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("filestore: write %s: %w", path, err)
	}
	return nil
}

// loadObjects reads a JSONL file and decodes each line into the right concrete
// type by Kind, keeping the last occurrence of each ID (last-wins).
func loadObjects(path string) ([]Entity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("filestore: read %s: %w", path, err)
	}
	byID := make(map[string]Entity)
	var order []string
	for _, line := range splitLines(raw) {
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Kind string `json:"Kind"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue // skip malformed lines
		}
		obj, err := decodeByKind(probe.Kind, line)
		if err != nil {
			continue
		}
		e := obj.(Entity)
		if _, seen := byID[e.ObjectID()]; !seen {
			order = append(order, e.ObjectID())
		}
		byID[e.ObjectID()] = e
	}
	out := make([]Entity, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}

// decodeByKind unmarshals a line into the concrete type its Kind names.
func decodeByKind(kind string, line []byte) (any, error) {
	switch kind {
	case "collective":
		var c Collective
		return &c, json.Unmarshal(line, &c)
	case "incursion":
		var i Incursion
		return &i, json.Unmarshal(line, &i)
	case "transmission":
		var t Transmission
		return &t, json.Unmarshal(line, &t)
	case "probe":
		var p Probe
		return &p, json.Unmarshal(line, &p)
	case "pattern":
		var p Pattern
		return &p, json.Unmarshal(line, &p)
	case "synapse":
		var s Synapse
		return &s, json.Unmarshal(line, &s)
	case "directive":
		var d Directive
		return &d, json.Unmarshal(line, &d)
	case "objective":
		var o Objective
		return &o, json.Unmarshal(line, &o)
	case "artifact":
		var a Artifact
		return &a, json.Unmarshal(line, &a)
	}
	var envelope struct {
		Body json.RawMessage
	}
	if err := json.Unmarshal(line, &envelope); err == nil && envelope.Body != nil {
		var drone Drone
		return &drone, json.Unmarshal(line, &drone)
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

// flattenTo narrows []Entity from []any for RebuildLinks.
func flattenTo(objs []Entity) []Entity { return objs }

func splitLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			out = append(out, raw[start:i])
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, raw[start:])
	}
	return out
}

// ChatSummary is the lightweight metadata the Continue screen lists chats by.
type ChatSummary struct {
	ID         string
	Title      string // first user prompt, truncated
	Model      string // the model that drove the first turn (if recorded)
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Incursions int
	Messages   int // transmissions + probes, a rough size proxy
}

// ListCollectives on the FileStore delegates to the package function so
// FileStore satisfies ChatStore. The package function stays — MigrateLegacyChats
// calls it with an explicit dir.
func (s *FileStore) ListCollectives() ([]ChatSummary, error) {
	return ListCollectives(s.dir)
}

// ListCollectives scans dir for chat files and returns their summaries, most
// recently touched first. Malformed files are skipped rather than failing the
// whole listing.
func ListCollectives(dir string) ([]ChatSummary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ChatSummary
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		id := e.Name()[:len(e.Name())-len(".jsonl")]
		sum, ok := summarize(filepath.Join(dir, id+".jsonl"), id)
		if !ok {
			continue
		}
		out = append(out, sum)
	}
	// Most-recently-touched first.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].UpdatedAt.After(out[i].UpdatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// summarize scans one chat file for its metadata without rebuilding the graph.
func summarize(path, id string) (ChatSummary, bool) {
	objs, err := loadObjects(path)
	if err != nil {
		return ChatSummary{}, false
	}
	var sum ChatSummary
	sum.ID = id
	sum.Title = "(empty)"
	for _, o := range objs {
		v := o.GetVinculum()
		if sum.CreatedAt.IsZero() || v.CreatedAt.Before(sum.CreatedAt) {
			sum.CreatedAt = v.CreatedAt
		}
		if v.UpdatedAt.After(sum.UpdatedAt) {
			sum.UpdatedAt = v.UpdatedAt
		}
		switch x := o.(type) {
		case *Collective:
			if x.CreatedAt.After(sum.UpdatedAt) {
				sum.UpdatedAt = x.CreatedAt
			}
			if x.Title != "" {
				sum.Title = truncateTitle(x.Title) // a generated name beats the first prompt
			}
		case *Incursion:
			sum.Incursions++
			if sum.Title == "(empty)" && x.Prompt != "" {
				sum.Title = truncateTitle(x.Prompt)
			}
		case *Transmission:
			sum.Messages++
		case *Probe:
			sum.Messages++
		}
	}
	if sum.CreatedAt.IsZero() {
		return ChatSummary{}, false
	}
	return sum, true
}

func truncateTitle(s string) string {
	s = firstLine(s)
	if len([]rune(s)) > 60 {
		return string([]rune(s)[:59]) + "…"
	}
	return s
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
