package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// EntryRecord is one tracked cron entry. Host-scoped and versioned so a
// future server can merge per-host registry files into a fleet view ("manage
// huge workflows with many clusters").
type EntryRecord struct {
	Hash      string    `json:"hash"`
	Schedule  string    `json:"schedule"`
	Command   string    `json:"command"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Note      string    `json:"note,omitempty"` // user annotation
}

// Registry is the local tracking store (Dir/registry.json, atomic, 0600).
type Registry struct {
	Version    int           `json:"version"`
	Host       string        `json:"host"`
	User       string        `json:"user"`
	UpdatedAt  time.Time     `json:"updated_at"`
	Entries    []EntryRecord `json:"entries"`
	LastBackup string        `json:"last_backup,omitempty"`
}

func LoadRegistry(dir string) (Registry, error) {
	var r Registry
	b, err := os.ReadFile(filepath.Join(dir, "registry.json"))
	if os.IsNotExist(err) {
		return Registry{Version: 1}, nil
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("cron registry: %w", err)
	}
	return r, nil
}

// Observe folds a fresh parse into the registry: new entries get FirstSeen,
// known entries get LastSeen stamped. Removed entries are NOT dropped —
// DiffSinceLast reports them so the UI can show "removed since last seen".
func (r *Registry) Observe(lines []Line, now time.Time) {
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Kind != LineEntry {
			continue
		}
		seen[l.Hash] = true
		rec := r.find(l.Hash)
		if rec == nil {
			r.Entries = append(r.Entries, EntryRecord{
				Hash: l.Hash, Schedule: l.Schedule, Command: l.Command,
				FirstSeen: now, LastSeen: now,
			})
			continue
		}
		rec.LastSeen = now
		rec.Schedule, rec.Command = l.Schedule, l.Command
	}
	_ = seen
}

func (r *Registry) find(hash string) *EntryRecord {
	for i := range r.Entries {
		if r.Entries[i].Hash == hash {
			return &r.Entries[i]
		}
	}
	return nil
}

// DiffSinceLast compares a fresh parse against the registry's entry set:
// added = present now but never seen (or FirstSeen == now), removed = tracked
// but no longer present.
func (r *Registry) DiffSinceLast(lines []Line) (added, removed []EntryRecord) {
	present := map[string]bool{}
	for _, l := range lines {
		if l.Kind == LineEntry {
			present[l.Hash] = true
		}
	}
	for _, rec := range r.Entries {
		if !present[rec.Hash] {
			removed = append(removed, rec)
		}
	}
	for _, l := range lines {
		if l.Kind != LineEntry {
			continue
		}
		if rec := r.find(l.Hash); rec == nil {
			added = append(added, EntryRecord{Hash: l.Hash, Schedule: l.Schedule, Command: l.Command})
		}
	}
	return added, removed
}

// Save writes the registry atomically (temp + rename, 0600).
func (r *Registry) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if r.Version == 0 {
		r.Version = 1
	}
	if r.Host == "" {
		r.Host, _ = os.Hostname()
	}
	if r.User == "" {
		r.User = os.Getenv("USER")
	}
	r.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".registry.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "registry.json"))
}
