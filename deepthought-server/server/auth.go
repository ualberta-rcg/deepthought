package server

// Phase-1 auth: login with a shared password; the username (supplied by the
// client) becomes the session identity. Sessions are stored in MySQL when the
// database is configured (so a restart keeps everyone logged in) and in
// memory otherwise. Only the SHA-256 of a token is kept. Expiry slides: a
// session used in the second half of its lifetime is extended. Real per-user
// auth remains the documented next step; the login handler is the single
// place to change.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"sort"
	"sync"
	"time"

	"deepthought-server/store"
)

// Session is one logged-in identity.
type Session struct {
	User    string
	Expires time.Time
	// ID is the listing id of this session (store.SessionID).
	ID string
}

// SessionBackend persists sessions by token hash.
type SessionBackend interface {
	PutSession(hash [32]byte, r store.SessionRecord) error
	GetSession(hash [32]byte) (store.SessionRecord, error)
	TouchSession(hash [32]byte, lastSeen, expires time.Time) error
	DeleteSession(hash [32]byte) error
	DeleteUserSession(userID, id string) (bool, error)
	ListSessions(userID string) ([]store.SessionRow, error)
	PurgeSessions(now time.Time) (int64, error)
}

// seenEvery bounds last-seen writes to one per session per interval.
const seenEvery = 5 * time.Minute

// SessionStore issues and validates opaque session tokens.
type SessionStore struct {
	ttl     time.Duration
	backend SessionBackend
}

// NewSessionStore returns an in-memory store whose sessions live for ttl.
func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{ttl: ttl, backend: newMemSessions()}
}

// UseBackend moves session storage (e.g. to MySQL). Call before serving.
func (s *SessionStore) UseBackend(b SessionBackend) { s.backend = b }

func tokenHash(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// Login issues a new session token for user ("" on failure: auth fails closed).
func (s *SessionStore) Login(user string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	token := hex.EncodeToString(b)
	now := time.Now()
	rec := store.SessionRecord{UserID: sanitizeUserID(user), Name: user, Created: now, LastSeen: now, Expires: now.Add(s.ttl)}
	if err := s.backend.PutSession(tokenHash(token), rec); err != nil {
		log.Printf("session store: %v", err)
		return ""
	}
	return token
}

// Valid reports the session for token, if present and unexpired, sliding
// the expiry forward when the session is past half its lifetime.
func (s *SessionStore) Valid(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	hash := tokenHash(token)
	rec, err := s.backend.GetSession(hash)
	if err != nil {
		if !errors.Is(err, store.ErrNoSession) {
			log.Printf("session store: %v", err)
		}
		return Session{}, false
	}
	now := time.Now()
	if now.After(rec.Expires) {
		_ = s.backend.DeleteSession(hash)
		return Session{}, false
	}
	slide := rec.Expires.Sub(now) < s.ttl/2
	if slide || now.Sub(rec.LastSeen) >= seenEvery {
		if slide {
			rec.Expires = now.Add(s.ttl)
		}
		if err := s.backend.TouchSession(hash, now, rec.Expires); err != nil {
			log.Printf("session store: %v", err)
		}
	}
	return Session{User: rec.Name, Expires: rec.Expires, ID: store.SessionID(hash)}, true
}

// Revoke drops a session (logout).
func (s *SessionStore) Revoke(token string) {
	if err := s.backend.DeleteSession(tokenHash(token)); err != nil {
		log.Printf("session store: %v", err)
	}
}

// List returns user's sessions, most recently used first.
func (s *SessionStore) List(user string) ([]store.SessionRow, error) {
	return s.backend.ListSessions(sanitizeUserID(user))
}

// RevokeID drops one of user's sessions by listing id.
func (s *SessionStore) RevokeID(user, id string) (bool, error) {
	return s.backend.DeleteUserSession(sanitizeUserID(user), id)
}

// Purge removes expired sessions.
func (s *SessionStore) Purge() (int64, error) { return s.backend.PurgeSessions(time.Now()) }

// memSessions is the no-database backend.
type memSessions struct {
	mu sync.Mutex
	m  map[[32]byte]store.SessionRecord
}

func newMemSessions() *memSessions { return &memSessions{m: map[[32]byte]store.SessionRecord{}} }

func (b *memSessions) PutSession(hash [32]byte, r store.SessionRecord) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.m[hash] = r
	return nil
}

func (b *memSessions) GetSession(hash [32]byte) (store.SessionRecord, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.m[hash]
	if !ok {
		return store.SessionRecord{}, store.ErrNoSession
	}
	return r, nil
}

func (b *memSessions) TouchSession(hash [32]byte, lastSeen, expires time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r, ok := b.m[hash]; ok {
		r.LastSeen, r.Expires = lastSeen, expires
		b.m[hash] = r
	}
	return nil
}

func (b *memSessions) DeleteSession(hash [32]byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, hash)
	return nil
}

func (b *memSessions) DeleteUserSession(userID, id string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for h, r := range b.m {
		if r.UserID == userID && store.SessionID(h) == id {
			delete(b.m, h)
			return true, nil
		}
	}
	return false, nil
}

func (b *memSessions) ListSessions(userID string) ([]store.SessionRow, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []store.SessionRow{}
	for h, r := range b.m {
		if r.UserID == userID {
			out = append(out, store.SessionRow{ID: store.SessionID(h), Created: r.Created.UTC(), LastSeen: r.LastSeen.UTC(), Expires: r.Expires.UTC()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}

func (b *memSessions) PurgeSessions(now time.Time) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var n int64
	for h, r := range b.m {
		if now.After(r.Expires) {
			delete(b.m, h)
			n++
		}
	}
	return n, nil
}
