package store

// Provider credentials, per user. `env` credentials are references such as
// $TYK_KEY and are stored as written; `secret` credentials arrive already
// sealed by the server vault (this package never sees a plaintext key).
// Fingerprints are HMACs under a per-user salt so a client can compare its
// local key with the server copy without either side revealing it.

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"time"
)

// ErrNoCredential is returned when a credential id is not on file.
var ErrNoCredential = errors.New("no such credential")

// Credential kinds.
const (
	CredentialEnv    = "env"
	CredentialSecret = "secret"
)

// CredentialRow is the index entry: identity and fingerprint, never a value.
type CredentialRow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	BaseURL     string    `json:"base_url"`
	Wire        string    `json:"wire"`
	Kind        string    `json:"kind"`
	Fingerprint string    `json:"fingerprint"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// StoredCredential is a row with its stored material.
type StoredCredential struct {
	CredentialRow
	EnvRef            string
	Nonce, Ciphertext []byte
}

// CredentialDB stores credentials in the shared pool.
type CredentialDB struct{ db *sql.DB }

func NewCredentialDB(db *sql.DB) *CredentialDB { return &CredentialDB{db: db} }

// Salt returns userID's fingerprint salt, creating it on first use.
func (c *CredentialDB) Salt(userID string) ([]byte, error) {
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	if _, err := c.db.Exec(`INSERT IGNORE INTO credential_salts(user_id,salt) VALUES(?,?)`, userID, fresh); err != nil {
		return nil, err
	}
	var salt []byte
	err := c.db.QueryRow(`SELECT salt FROM credential_salts WHERE user_id=?`, userID).Scan(&salt)
	return salt, err
}

func (c *CredentialDB) List(userID string) ([]CredentialRow, error) {
	rows, err := c.db.Query(`SELECT id,name,base_url,wire,kind,fingerprint,updated_at FROM credentials WHERE user_id=? ORDER BY name, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CredentialRow{}
	for rows.Next() {
		var r CredentialRow
		var updated int64
		if err := rows.Scan(&r.ID, &r.Name, &r.BaseURL, &r.Wire, &r.Kind, &r.Fingerprint, &updated); err != nil {
			return nil, err
		}
		r.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *CredentialDB) Get(userID, id string) (StoredCredential, error) {
	var s StoredCredential
	var updated int64
	var envRef sql.NullString
	err := c.db.QueryRow(`SELECT id,name,base_url,wire,kind,fingerprint,updated_at,env_ref,nonce,ciphertext FROM credentials WHERE user_id=? AND id=?`, userID, id).
		Scan(&s.ID, &s.Name, &s.BaseURL, &s.Wire, &s.Kind, &s.Fingerprint, &updated, &envRef, &s.Nonce, &s.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredCredential{}, ErrNoCredential
	}
	if err != nil {
		return StoredCredential{}, err
	}
	s.EnvRef = envRef.String
	s.UpdatedAt = time.Unix(updated, 0).UTC()
	return s, nil
}

// Put inserts or replaces userID's credential s.ID.
func (c *CredentialDB) Put(userID string, s StoredCredential) error {
	var envRef any
	if s.Kind == CredentialEnv {
		envRef = s.EnvRef
	}
	_, err := c.db.Exec(`
INSERT INTO credentials(user_id,id,name,base_url,wire,kind,env_ref,nonce,ciphertext,fingerprint,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)
AS new
ON DUPLICATE KEY UPDATE name=new.name, base_url=new.base_url, wire=new.wire, kind=new.kind, env_ref=new.env_ref,
  nonce=new.nonce, ciphertext=new.ciphertext, fingerprint=new.fingerprint, updated_at=new.updated_at`,
		userID, s.ID, s.Name, s.BaseURL, s.Wire, s.Kind, envRef, s.Nonce, s.Ciphertext, s.Fingerprint, time.Now().Unix())
	return err
}

func (c *CredentialDB) Delete(userID, id string) (bool, error) {
	res, err := c.db.Exec(`DELETE FROM credentials WHERE user_id=? AND id=?`, userID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
