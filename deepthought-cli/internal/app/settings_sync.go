package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/credential"
	"deepthought-cli/internal/unimatrix"
)

type syncRecord struct {
	Base        config.SharedDocument
	Revision    int64
	LastSuccess time.Time
	LastError   string
	Conflicts   []config.SyncConflict
	Choices     map[string]config.SyncChoice
}
type SyncStatus struct {
	State, Detail string
	LastSuccess   time.Time
	Conflicts     []config.SyncConflict
}

// SettingsSync serializes transfers. Pending work is the durable saved document
// minus Base, so a process crash cannot lose a separate in-memory upload queue.
type SettingsSync struct {
	mu       sync.Mutex
	stateMu  sync.Mutex
	live     *Settings
	session  *ServerSession
	key      string
	record   syncRecord
	next     time.Time
	observed config.SharedDocument
	ctx      context.Context
	cancel   context.CancelFunc
	stopped  bool
}

func (s *Settings) Saved() (config.File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.local == nil {
		return cloneFile(s.cfg.File), nil
	}
	cfg, err := s.local.Saved()
	if err != nil {
		return config.File{}, err
	}
	return cfg.File, nil
}
func (s *Settings) MirrorNotice() string {
	if s.local == nil {
		return ""
	}
	return s.local.MirrorNotice()
}
func (s *Settings) ImportFile() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.local == nil {
		return fmt.Errorf("database-backed settings required")
	}
	cfg, err := s.local.ImportFile()
	if err != nil {
		return err
	}
	s.cfg, s.revision, s.pool = cfg, cfg.Revision, unimatrix.NewPool()
	return nil
}
func (s *Settings) SyncWorker(session *ServerSession) *SettingsSync {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(s.path+"|"+session.URL+"|"+session.User)))
	if s.syncWorkers == nil {
		s.syncWorkers = map[string]*SettingsSync{}
	}
	if w := s.syncWorkers[key]; w != nil {
		w.stateMu.Lock()
		if !w.stopped {
			w.session = session
			w.stateMu.Unlock()
			return w
		}
		w.stateMu.Unlock()
	}
	w := &SettingsSync{live: s, session: session, key: key}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	if s.local != nil {
		_ = s.local.ReadRecord("settings-sync", key, &w.record)
	}
	s.syncWorkers[key] = w
	return w
}

// Stop cancels network work and serializes with local application of a response.
// A request already accepted by the server cannot be undone by disconnecting.
func (w *SettingsSync) Stop() {
	w.live.mu.Lock()
	defer w.live.mu.Unlock()
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.stopped = true
	w.cancel()
}

func (w *SettingsSync) connected(session *ServerSession) bool {
	if w.ctx.Err() != nil {
		return false
	}
	f, err := w.live.Saved()
	return err == nil && f.Server != nil && f.Server.URL == session.URL && f.Server.User == session.User
}
func (w *SettingsSync) persist() error {
	if w.live.local == nil {
		return fmt.Errorf("database-backed settings required for durable sync")
	}
	return w.live.local.WriteRecord("settings-sync", w.key, w.record)
}
func (w *SettingsSync) Status() SyncStatus {
	w.stateMu.Lock()
	st := SyncStatus{State: "Synced", LastSuccess: w.record.LastSuccess, Detail: w.record.LastError, Conflicts: append([]config.SyncConflict(nil), w.record.Conflicts...)}
	base := w.record.Base
	w.stateMu.Unlock()
	if len(st.Conflicts) > 0 || st.Detail != "" {
		st.State = "Needs attention"
		return st
	}
	f, err := w.live.Saved()
	if err != nil {
		st.State = "Needs attention"
		st.Detail = "Local settings unavailable"
		return st
	}
	if base == nil || !config.SameShared(config.Shared(f), base) {
		st.State = "Sync pending"
	}
	return st
}
func (w *SettingsSync) Due(now time.Time) bool {
	f, err := w.live.Saved()
	if err != nil {
		return false
	}
	// A connection is bound to the explicitly selected account and endpoint.
	w.stateMu.Lock()
	matches := !w.stopped && f.Server != nil && f.Server.URL == w.session.URL && f.Server.User == w.session.User
	w.stateMu.Unlock()
	if !matches {
		return false
	}
	doc := config.Shared(f)
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.observed == nil || !config.SameShared(doc, w.observed) {
		w.observed = doc
		w.next = now.Add(time.Second)
	}
	return !now.Before(w.next)
}
func (w *SettingsSync) Resolve(path, side string) error {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if side != "local" && side != "remote" {
		return fmt.Errorf("invalid resolution")
	}
	for _, c := range w.record.Conflicts {
		if c.Path == path {
			if w.record.Choices == nil {
				w.record.Choices = map[string]config.SyncChoice{}
			}
			w.record.Choices[path] = config.SyncChoice{Fingerprint: c.Fingerprint(), Side: side}
			w.next = time.Time{}
			return w.persist()
		}
	}
	return fmt.Errorf("conflict changed; refresh before resolving")
}
func (w *SettingsSync) Run() SyncStatus {
	if !w.mu.TryLock() {
		return w.Status()
	}
	defer w.mu.Unlock()
	w.stateMu.Lock()
	session := *w.session
	session.ctx = w.ctx
	w.stateMu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		if !w.connected(&session) {
			return SyncStatus{State: "Disconnected", Detail: "Connection changed; reconnect to synchronize."}
		}
		remote, revision, err := session.fetchUserSettings()
		if err != nil {
			return w.failed(err)
		}
		source := remote
		if source == nil {
			source = session.Defaults
		}
		r, err := config.ReadShared(source)
		if err != nil {
			return w.failed(err)
		}
		r = config.SharedWithDefaults(r)
		saved, err := w.live.Saved()
		if err != nil {
			return w.failed(err)
		}
		local := config.Shared(saved)
		w.stateMu.Lock()
		base := w.record.Base
		choices := map[string]config.SyncChoice{}
		for k, v := range w.record.Choices {
			choices[k] = v
		}
		w.stateMu.Unlock()
		if base == nil {
			base = config.Shared(config.Defaults().File)
		}
		merged, conflicts := config.MergeShared(base, local, r, choices)
		if len(conflicts) > 0 {
			w.stateMu.Lock()
			w.record.Conflicts = conflicts
			w.record.LastError = "Choose local or server values for conflicting settings."
			_ = w.persist()
			w.next = time.Now().Add(5 * time.Minute)
			w.observed = local
			w.stateMu.Unlock()
			return w.Status()
		}
		if _, err = config.ApplyShared(saved, merged); err != nil {
			return w.failed(err)
		}
		if remote == nil || !config.SameShared(merged, r) {
			if !w.connected(&session) {
				return SyncStatus{State: "Disconnected", Detail: "Connection changed; reconnect to synchronize."}
			}
			_, err = session.pushUserSettings(map[string]any(merged), revision)
			if isRevisionConflict(err) {
				continue
			}
			if err != nil {
				return w.failed(err)
			}
		}
		// Read back the accepted document; another client may have committed too.
		accepted, ackRevision, err := session.fetchUserSettings()
		if err != nil {
			return w.failed(err)
		}
		ack, err := config.ReadShared(accepted)
		if err != nil {
			return w.failed(err)
		}
		ack = config.SharedWithDefaults(ack)
		// Apply while holding the live settings lock. Edits made during network I/O
		// are merged against the original local snapshot and remain pending if needed.
		if err = w.live.applySynced(local, ack, &session); err != nil {
			return w.failed(err)
		}
		w.stateMu.Lock()
		w.record = syncRecord{Base: ack, Revision: ackRevision, LastSuccess: time.Now()}
		err = w.persist()
		w.next = time.Now().Add(5 * time.Minute)
		w.observed = local
		w.stateMu.Unlock()
		if err != nil {
			return w.failed(fmt.Errorf("could not record successful sync; retry required"))
		}
		return w.Status()
	}
	return w.failed(fmt.Errorf("server settings kept changing; changes remain pending, retry later"))
}
func (w *SettingsSync) failed(err error) SyncStatus {
	w.stateMu.Lock()
	w.record.LastError = credential.Redact(err.Error())
	w.next = time.Now().Add(5 * time.Minute)
	_ = w.persist()
	w.stateMu.Unlock()
	return w.Status()
}
func (s *Settings) applySynced(start, remote config.SharedDocument, session *ServerSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.local == nil {
		return fmt.Errorf("database-backed settings required")
	}
	saved, err := s.local.Saved()
	if err != nil {
		return err
	}
	if session.ctx.Err() != nil || saved.Server == nil || saved.Server.URL != session.URL || saved.Server.User != session.User {
		return fmt.Errorf("connection changed; downloaded settings were not applied")
	}
	// Concurrent local conflicts deliberately stay local and become the next delta.
	merged, _ := config.MergeShared(start, config.Shared(saved.File), remote, nil)
	f, err := config.ApplyShared(saved.File, merged)
	if err != nil {
		return err
	}
	cfg, err := s.local.SaveSynced(saved.File, f)
	if err != nil {
		return err
	}
	s.cfg, s.revision, s.pool = cfg, cfg.Revision, unimatrix.NewPool()
	return nil
}
