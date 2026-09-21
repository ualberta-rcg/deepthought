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
