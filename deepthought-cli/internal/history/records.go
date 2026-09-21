package history

import (
	"encoding/json"
	"fmt"
	"time"
)

type Record struct {
	ID      string          `json:"id"`
	Data    json.RawMessage `json:"data"`
	Updated time.Time       `json:"updated"`
}

// ClaimRecord atomically journals an operation before its external side effect.
func (s *SQLiteStore) ClaimRecord(kind, id string, value any) (bool, error) {
	if err := s.ensureRecords(); err != nil {
		return false, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`INSERT INTO local_records(kind,id,data,updated) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO NOTHING`, kind, id, raw, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *SQLiteStore) ensureRecords() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS local_records(kind TEXT NOT NULL,id TEXT NOT NULL,data BLOB NOT NULL,updated TEXT NOT NULL,PRIMARY KEY(kind,id))`)
	return err
}
func (s *SQLiteStore) PutRecord(kind, id string, value any) error {
	if kind == "" || id == "" {
		return fmt.Errorf("record kind and id required")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := s.ensureRecords(); err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO local_records(kind,id,data,updated) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data,updated=excluded.updated`, kind, id, raw, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *SQLiteStore) GetRecord(kind, id string, value any) error {
	if err := s.ensureRecords(); err != nil {
		return err
	}
	var raw []byte
	if err := s.db.QueryRow(`SELECT data FROM local_records WHERE kind=? AND id=?`, kind, id).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}

// ReplaceRecord prevents concurrent workflow editors from silently losing changes.
func (s *SQLiteStore) ReplaceRecord(kind, id string, revision int, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`UPDATE local_records SET data=?,updated=? WHERE kind=? AND id=? AND json_extract(data,'$.revision')=?`, raw, time.Now().UTC().Format(time.RFC3339Nano), kind, id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("record changed concurrently; reload before retrying")
	}
	return nil
}
func (s *SQLiteStore) Records(kind string) ([]Record, error) {
	if err := s.ensureRecords(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,data,updated FROM local_records WHERE kind=? ORDER BY updated DESC LIMIT 1000`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var stamp string
		if err := rows.Scan(&r.ID, &r.Data, &stamp); err != nil {
			return nil, err
		}
		r.Updated, _ = time.Parse(time.RFC3339Nano, stamp)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *SQLiteStore) DeleteRecord(kind, id string) error {
	if err := s.ensureRecords(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM local_records WHERE kind=? AND id=?`, kind, id)
	return err
}
