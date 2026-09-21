package store

// The records KV on MySQL (port of the SQLite local_records in records.go),
// user-scoped: every key lives under (user_id, kind, id), so workflows and
// journals rove with the user and never collide across tenants.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// ClaimRecord atomically journals an operation before its external side effect.
func (s *MySQLStore) ClaimRecord(kind, id string, value any) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`INSERT IGNORE INTO records(user_id,kind,id,data,updated) VALUES(?,?,?,?,?)`,
		s.userID, kind, id, raw, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *MySQLStore) PutRecord(kind, id string, value any) error {
	if kind == "" || id == "" {
		return fmt.Errorf("record kind and id required")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
INSERT INTO records(user_id,kind,id,data,updated) VALUES(?,?,?,?,?)
AS new
ON DUPLICATE KEY UPDATE data=new.data, updated=new.updated`,
		s.userID, kind, id, raw, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *MySQLStore) GetRecord(kind, id string, value any) error {
	var raw []byte
	if err := s.db.QueryRow(`SELECT data FROM records WHERE user_id=? AND kind=? AND id=?`, s.userID, kind, id).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}

// ReplaceRecord prevents concurrent workflow editors from silently losing changes.
func (s *MySQLStore) ReplaceRecord(kind, id string, revision int, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`UPDATE records SET data=?,updated=? WHERE user_id=? AND kind=? AND id=? AND data->>'$.revision'=?`,
		raw, time.Now().UTC().Format(time.RFC3339Nano), s.userID, kind, id, strconv.Itoa(revision))
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

func (s *MySQLStore) Records(kind string) ([]Record, error) {
	rows, err := s.db.Query(`SELECT id,data,updated FROM records WHERE user_id=? AND kind=? ORDER BY updated DESC LIMIT 1000`, s.userID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []history.Record
	for rows.Next() {
		var r history.Record
		var stamp string
		if err := rows.Scan(&r.ID, &r.Data, &stamp); err != nil {
			return nil, err
		}
		r.Updated, _ = time.Parse(time.RFC3339Nano, stamp)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *MySQLStore) DeleteRecord(kind, id string) error {
	_, err := s.db.Exec(`DELETE FROM records WHERE user_id=? AND kind=? AND id=?`, s.userID, kind, id)
	return err
}
