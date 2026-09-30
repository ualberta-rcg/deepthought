package store

// Login sessions on MySQL. Only the SHA-256 of a token is stored, so a
// database dump cannot be replayed as a login. Times are unix seconds so the
// expiry purge can compare numerically.

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ErrNoSession is returned when a token hash is not on file.
var ErrNoSession = errors.New("no such session")

// SessionRecord is one stored login.
type SessionRecord struct {
	UserID, Name               string
	Created, LastSeen, Expires time.Time
}

// SessionRow is a session as listed to its owner. ID is the first 16 hex
// digits of the token hash, enough to revoke it and useless to log in.
type SessionRow struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
	Expires  time.Time `json:"expires"`
}

// SessionID is the listing/revocation id for a token hash.
func SessionID(hash [32]byte) string { return hex.EncodeToString(hash[:8]) }

// SessionDB stores sessions in the shared pool.
type SessionDB struct{ db *sql.DB }

func NewSessionDB(db *sql.DB) *SessionDB { return &SessionDB{db: db} }

func (s *SessionDB) PutSession(hash [32]byte, r SessionRecord) error {
	_, err := s.db.Exec(`INSERT INTO sessions(token_hash,user_id,name,created_at,last_seen,expires_at) VALUES(?,?,?,?,?,?)`,
		hash[:], r.UserID, r.Name, r.Created.Unix(), r.LastSeen.Unix(), r.Expires.Unix())
	return err
}

func (s *SessionDB) GetSession(hash [32]byte) (SessionRecord, error) {
	var r SessionRecord
	var created, seen, expires int64
	err := s.db.QueryRow(`SELECT user_id,name,created_at,last_seen,expires_at FROM sessions WHERE token_hash=?`, hash[:]).
		Scan(&r.UserID, &r.Name, &created, &seen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, ErrNoSession
	}
	if err != nil {
		return SessionRecord{}, err
	}
	r.Created, r.LastSeen, r.Expires = time.Unix(created, 0), time.Unix(seen, 0), time.Unix(expires, 0)
	return r, nil
}

func (s *SessionDB) TouchSession(hash [32]byte, lastSeen, expires time.Time) error {
	_, err := s.db.Exec(`UPDATE sessions SET last_seen=?, expires_at=? WHERE token_hash=?`, lastSeen.Unix(), expires.Unix(), hash[:])
	return err
}

func (s *SessionDB) DeleteSession(hash [32]byte) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, hash[:])
	return err
}

// DeleteUserSession revokes one of userID's sessions by listing id.
func (s *SessionDB) DeleteUserSession(userID, id string) (bool, error) {
	if len(id) != 16 {
		return false, nil
	}
	res, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=? AND LEFT(HEX(token_hash),16)=?`, userID, strings.ToUpper(id))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *SessionDB) ListSessions(userID string) ([]SessionRow, error) {
	rows, err := s.db.Query(`SELECT token_hash,created_at,last_seen,expires_at FROM sessions WHERE user_id=? ORDER BY last_seen DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionRow{}
	for rows.Next() {
		var raw []byte
		var created, seen, expires int64
		if err := rows.Scan(&raw, &created, &seen, &expires); err != nil {
			return nil, err
		}
		var hash [32]byte
		copy(hash[:], raw)
		out = append(out, SessionRow{ID: SessionID(hash), Created: time.Unix(created, 0).UTC(), LastSeen: time.Unix(seen, 0).UTC(), Expires: time.Unix(expires, 0).UTC()})
	}
	return out, rows.Err()
}

// PurgeSessions deletes every session that expired before now.
func (s *SessionDB) PurgeSessions(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
