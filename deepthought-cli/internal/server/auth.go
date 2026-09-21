package server

// Phase-1 auth: login with a shared password; the username (supplied by the
// client) becomes the session identity. Sessions live in memory with an
// expiry — a server restart logs everyone out, which is acceptable for the
// single-user phase. Real per-user auth (SSH-key-derived or site SSO) remains
// the documented next step; the login handler is the single place to change.

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Session is one logged-in identity.
type Session struct {
	User    string
	Expires time.Time
}

// SessionStore issues and validates opaque session tokens.
type SessionStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	sessions map[string]Session
}

// NewSessionStore returns a store whose sessions live for ttl.
func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{ttl: ttl, sessions: map[string]Session{}}
}

// Login issues a new session token for user.
func (s *SessionStore) Login(user string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is unrecoverable; an empty token is rejected
		// by Valid, so auth fails closed.
		return ""
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[token] = Session{User: user, Expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return token
}

// Valid reports the session for token, if present and unexpired.
func (s *SessionStore) Valid(token string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return Session{}, false
	}
	if time.Now().After(sess.Expires) {
		delete(s.sessions, token)
		return Session{}, false
	}
	return sess, true
}

// Revoke drops a session (logout).
func (s *SessionStore) Revoke(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}
