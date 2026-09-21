package history

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Retain the original table's columns for older processes. Only the erroneous
// content uniqueness constraint changes; event identity is the primary key.
func (s *SQLiteStore) migrateIdentity(path string) error {
	var schema string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='interactions'`).Scan(&schema); err != nil {
		return err
	}
	constraint := regexp.MustCompile(`(?i),\s*UNIQUE\s*\(\s*session_id\s*,\s*body_hash\s*\)`)
	if !constraint.MatchString(schema) {
		return nil
	}
	backup := path + ".before-identity-v2"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		if _, err := s.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(backup, "'", "''") + `'`); err != nil {
			return fmt.Errorf("history backup: %w", err)
		}
		if err := os.Chmod(backup, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`ALTER TABLE interactions RENAME TO interactions_identity_v1`,
		constraint.ReplaceAllString(schema, ""),
		`INSERT INTO interactions SELECT * FROM interactions_identity_v1`,
		`DROP TABLE interactions_identity_v1`,
		`CREATE INDEX interactions_session_created ON interactions(session_id,created_at)`,
		`CREATE INDEX interactions_collective_kind_state ON interactions(collective_id,kind,state)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("history identity migration: %w", err)
		}
	}
	return tx.Commit()
}
