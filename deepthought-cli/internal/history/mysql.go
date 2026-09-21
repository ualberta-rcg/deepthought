package history

// MySQLStore is the server-side port of SQLiteStore: the same Borg-graph
// persistence (one polymorphic `interactions` row per Drone, canonical bodies,
// legacy_json as the rehydration source) behind the same ChatStore interface,
// but multi-user and networked. Differences from the SQLite original, all
// deliberate:
//
//   - every table and query is user-scoped (a store is constructed per user
//     via ForUser; the server gets the user from the login session)
//   - the content-addressed filesystem spill becomes a `bodies` table keyed
//     by sha256 (no server-local filesystem dependency)
//   - a normal connection pool replaces SetMaxOpenConns(1) — MySQL handles
//     concurrent writers natively; per-save transactions stay
//   - the write-only/unused SQLite tables (links, summaries, body_versions)
//     are not ported; drone_metadata (the one actually read) is
//   - timestamps stay UTC RFC3339Nano VARCHARs, preserving the lexicographic
//     ordering the queries rely on
//
// The users/user_settings tables (login identity + the roving settings layer)
// live here too, managed by UserStore.

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql" // register the driver
)

// Compile-time guarantee the MySQL store satisfies the same contracts.
var _ ChatStore = (*MySQLStore)(nil)
var _ UsageReporter = (*MySQLStore)(nil)

// MySQLStore is one user's view of the shared server database.
type MySQLStore struct {
	db     *sql.DB
	userID string
}

// OpenMySQL opens the pool and ensures the schema exists (idempotent).
func OpenMySQL(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetConnMaxLifetime(5 * time.Minute)
	for _, ddl := range mysqlSchema {
		if _, err := db.Exec(ddl); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("mysql schema: %w", err)
		}
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// ForUser returns a user-scoped store view over the shared pool.
func ForUser(db *sql.DB, userID string) *MySQLStore {
	return &MySQLStore{db: db, userID: userID}
}

var mysqlSchema = []string{
	`CREATE TABLE IF NOT EXISTS users (
	  id VARCHAR(64) NOT NULL PRIMARY KEY,
	  name VARCHAR(128) NOT NULL,
	  created_at VARCHAR(40) NOT NULL,
	  last_login_at VARCHAR(40) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS user_settings (
	  user_id VARCHAR(64) NOT NULL PRIMARY KEY,
	  settings JSON NOT NULL,
	  revision BIGINT NOT NULL DEFAULT 1,
	  updated_at VARCHAR(40) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS interactions (
	  id VARCHAR(72) NOT NULL PRIMARY KEY,
	  user_id VARCHAR(64) NOT NULL,
	  kind VARCHAR(32) NOT NULL,
	  session_id VARCHAR(72) NOT NULL,
	  collective_id VARCHAR(72),
	  parent_id VARCHAR(72),
	  body LONGBLOB,
	  body_ref VARCHAR(64),
	  body_hash BINARY(32) NOT NULL,
	  state VARCHAR(24) NOT NULL,
	  pinned TINYINT(1) NOT NULL DEFAULT 0,
	  poisoned TINYINT(1) NOT NULL DEFAULT 0,
	  tokens TEXT NOT NULL,
	  cost TEXT NOT NULL,
	  producer TEXT,
	  outcome VARCHAR(24),
	  fail_class VARCHAR(64),
	  sensitivity TINYINT NOT NULL DEFAULT 0,
	  use_count INT NOT NULL DEFAULT 0,
	  last_used VARCHAR(40),
	  created_at VARCHAR(40) NOT NULL,
	  updated_at VARCHAR(40),
	  legacy_json LONGTEXT,
	  KEY idx_interactions_user_coll (user_id, collective_id, kind, state),
	  KEY idx_interactions_user_created (user_id, created_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// The content-addressed blob spill, as a table instead of a directory.
	`CREATE TABLE IF NOT EXISTS bodies (
	  sha256 BINARY(32) NOT NULL PRIMARY KEY,
	  body LONGBLOB NOT NULL,
	  created_at VARCHAR(40) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS drone_metadata (
	  interaction_id VARCHAR(72) NOT NULL PRIMARY KEY,
	  summaries TEXT NOT NULL,
	  links TEXT NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	// The records KV (SQLite's local_records), user-scoped.
	`CREATE TABLE IF NOT EXISTS records (
	  user_id VARCHAR(64) NOT NULL,
	  kind VARCHAR(128) NOT NULL,
	  id VARCHAR(128) NOT NULL,
	  data JSON NOT NULL,
	  updated VARCHAR(40) NOT NULL,
	  PRIMARY KEY (user_id, kind, id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS migrations (
	  name VARCHAR(128) NOT NULL PRIMARY KEY,
	  completed_at VARCHAR(40) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
}

func (s *MySQLStore) CreateCollective(req SpawnCollectiveRequest) (*Collective, error) {
	coll := NewCollective(req)
	if err := s.SaveObject(coll); err != nil {
		return nil, err
	}
	return coll, nil
}

func (s *MySQLStore) SaveObject(obj Entity) error {
	if obj == nil {
		return errors.New("mysql store: nil object")
	}
	if drone, ok := obj.(*Drone); ok {
		return s.SaveDrone(context.Background(), drone, nil)
	}
	drone, err := legacyEntityDrone(obj)
	if err != nil {
		return err
	}
	legacy, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return s.SaveDrone(context.Background(), drone, legacy)
}

func (s *MySQLStore) SaveCollective(coll *Collective) error {
	if coll == nil {
		return errors.New("mysql store: nil collective")
	}
	for _, obj := range loadEntitiesWithPatterns(coll) {
		if err := s.SaveObject(obj); err != nil {
			return err
		}
	}
	return nil
}

// UsageByModel aggregates token usage across THIS USER's history.
func (s *MySQLStore) UsageByModel() (map[string]Cost, error) {
	const q = `SELECT
	  NULLIF(JSON_UNQUOTE(JSON_EXTRACT(producer,'$.model_id')),'') AS mid,
	  SUM(COALESCE(JSON_EXTRACT(cost,'$.input_tokens'),0)),
	  SUM(COALESCE(JSON_EXTRACT(cost,'$.output_tokens'),0))
	FROM interactions
	WHERE user_id=? AND kind IN ('transmission','incursion') AND producer IS NOT NULL AND JSON_VALID(producer)
	GROUP BY mid`
	rows, err := s.db.Query(q, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Cost{}
	for rows.Next() {
		var mid sql.NullString
		var in, outTokens sql.NullInt64
		if err := rows.Scan(&mid, &in, &outTokens); err != nil {
			return nil, err
		}
		if mid.Valid && mid.String != "" {
			out[mid.String] = Cost{InputTokens: int(in.Int64), OutputTokens: int(outTokens.Int64)}
		}
	}
	return out, rows.Err()
}

func (s *MySQLStore) SaveDrone(ctx context.Context, drone *Drone, legacy []byte) error {
	if drone == nil || drone.ID == "" {
		return errors.New("mysql store: empty drone")
	}
	{
		raw, hash, err := canonicalBody(json.RawMessage(drone.Body))
		if err != nil {
			return err
		}
		drone.Body, drone.BodyHash = raw, hash
	}
	body, bodyRef, err := s.inlineOrStore(drone.Body, drone.BodyHash)
	if err != nil {
		return err
	}
	tokens, _ := json.Marshal(drone.Tokens)
	cost, _ := json.Marshal(drone.Cost)
	producer, _ := json.Marshal(drone.Producer)
	var fail any
	if drone.FailClass != nil {
		fail = string(*drone.FailClass)
	}
	collectiveID := drone.CollectiveID
	if drone.Kind == "collective" {
		collectiveID = drone.ID
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// IDs are app-generated (UUIDv7/newID prefixes), so the global PK is safe;
	// user_id is written once and never re-homed on update.
	_, err = tx.ExecContext(ctx, `
INSERT INTO interactions
(id,user_id,kind,session_id,collective_id,parent_id,body,body_ref,body_hash,state,pinned,poisoned,tokens,cost,producer,outcome,fail_class,sensitivity,use_count,last_used,created_at,updated_at,legacy_json)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
AS new
ON DUPLICATE KEY UPDATE
 body=new.body, body_ref=new.body_ref, body_hash=new.body_hash, state=new.state,
 pinned=new.pinned, poisoned=new.poisoned, tokens=new.tokens,
 cost=new.cost, producer=new.producer, outcome=new.outcome,
 fail_class=new.fail_class, sensitivity=new.sensitivity,
 use_count=new.use_count, last_used=new.last_used,
 updated_at=new.updated_at, legacy_json=COALESCE(new.legacy_json, interactions.legacy_json)`,
		drone.ID, s.userID, drone.Kind, drone.SessionID, collectiveID, drone.ParentID,
		body, bodyRef, drone.BodyHash, drone.State, drone.Pinned, drone.Poisoned,
		string(tokens), string(cost), string(producer), drone.Outcome, fail,
		drone.Sensitivity, drone.UseCount, formatTimePtr(drone.LastUsed),
		formatTime(drone.CreatedAt), formatTime(drone.UpdatedAt), legacy)
	if err != nil {
		return fmt.Errorf("mysql store: save interaction: %w", err)
	}
	summaryJSON, err := json.Marshal(drone.Summaries)
	if err != nil {
		return err
	}
	linksJSON, err := json.Marshal(drone.Links)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO drone_metadata(interaction_id,summaries,links) VALUES(?,?,?)
AS new
ON DUPLICATE KEY UPDATE summaries=new.summaries, links=new.links`,
		drone.ID, string(summaryJSON), string(linksJSON))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// inlineOrStore keeps small bodies inline and moves >1 MiB bodies to the
// content-addressed bodies table (the MySQL replacement for the filesystem
// spill). bodyRef is the hex sha256; reads re-verify the hash.
func (s *MySQLStore) inlineOrStore(body, hash []byte) ([]byte, any, error) {
	if len(body) <= InlineBodyLimit {
		return body, nil, nil
	}
	ref := hex.EncodeToString(hash)
	_, err := s.db.Exec(`INSERT IGNORE INTO bodies(sha256,body,created_at) VALUES(?,?,?)`, hash, body, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, nil, err
	}
	return nil, ref, nil
}

func (s *MySQLStore) readBody(ref string, hash []byte) ([]byte, error) {
	var body []byte
	if err := s.db.QueryRow(`SELECT body FROM bodies WHERE sha256=?`, hash).Scan(&body); err != nil {
		return nil, fmt.Errorf("mysql store: body %s: %w", ref, err)
	}
	return body, nil
}

func (s *MySQLStore) GetDrone(ctx context.Context, id string) (*Drone, error) {
	row := s.db.QueryRowContext(ctx, `SELECT kind,session_id,collective_id,parent_id,body,body_ref,body_hash,state,pinned,poisoned,tokens,cost,producer,outcome,fail_class,sensitivity,use_count,last_used,created_at,updated FROM interactions WHERE id=? AND user_id=?`, id, s.userID)
	var d Drone
	d.ID = id
	var body, hash []byte
	var bodyRef, collectiveID, parentID, fail, lastUsed, updated sql.NullString
	var tokens, cost, producer, created string
	if err := row.Scan(&d.Kind, &d.SessionID, &collectiveID, &parentID, &body, &bodyRef, &hash, &d.State,
		&d.Pinned, &d.Poisoned, &tokens, &cost, &producer, &d.Outcome, &fail,
		&d.Sensitivity, &d.UseCount, &lastUsed, &created, &updated); err != nil {
		return nil, err
	}
	d.CollectiveID, d.ParentID, d.BodyHash = collectiveID.String, parentID.String, hash
	if bodyRef.Valid {
		var err error
		body, err = s.readBody(bodyRef.String, hash)
		if err != nil {
			return nil, err
		}
	}
	d.Body = body
	_, actualHash, hashErr := canonicalBody(json.RawMessage(body))
	if hashErr != nil {
		return nil, hashErr
	}
	d.BodyHash = actualHash
	_ = json.Unmarshal([]byte(tokens), &d.Tokens)
	_ = json.Unmarshal([]byte(cost), &d.Cost)
	_ = json.Unmarshal([]byte(producer), &d.Producer)
	if fail.Valid {
		fc := FailureClass(fail.String)
		d.FailClass = &fc
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if updated.Valid {
		d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated.String)
	}
	if lastUsed.Valid {
		parsed, _ := time.Parse(time.RFC3339Nano, lastUsed.String)
		d.LastUsed = &parsed
	}
	var summaryJSON, linksJSON string
	err := s.db.QueryRowContext(ctx, `SELECT summaries,links FROM drone_metadata WHERE interaction_id=?`, id).Scan(&summaryJSON, &linksJSON)
	if err == nil {
		if err := json.Unmarshal([]byte(summaryJSON), &d.Summaries); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(linksJSON), &d.Links); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return &d, nil
}

func (s *MySQLStore) GetCollective(id string) (*Collective, error) {
	rows, err := s.db.Query(`SELECT state, outcome, sensitivity, legacy_json FROM interactions WHERE user_id=? AND (id=? OR collective_id=?) AND legacy_json IS NOT NULL ORDER BY created_at`, s.userID, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entities []Entity
	for rows.Next() {
		var stateStr, outcomeStr string
		var sensInt int
		var raw []byte
		if err := rows.Scan(&stateStr, &outcomeStr, &sensInt, &raw); err != nil {
			return nil, err
		}
		var kind struct {
			Kind string `json:"Kind"`
		}
		if err := json.Unmarshal(raw, &kind); err != nil {
			return nil, fmt.Errorf("history %s: %w", id, err)
		}
		obj, err := decodeByKind(kind.Kind, raw)
		if err != nil {
			return nil, fmt.Errorf("history %s: %w", id, err)
		}
		e := obj.(Entity)
		v := e.GetVinculum()
		if stateStr != "" {
			v.State = State(stateStr)
		}
		if outcomeStr != "" {
			v.Outcome = Outcome(outcomeStr)
		}
		v.Sensitivity = Sensitivity(sensInt)
		entities = append(entities, e)
	}
	var coll *Collective
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, entity := range entities {
		if value, ok := entity.(*Collective); ok {
			coll = value
		}
	}
	if coll == nil {
		return nil, sql.ErrNoRows
	}
	coll.RebuildLinks(entities)
	return coll, nil
}

func (s *MySQLStore) Resume(id string) (*Collective, error) {
	return s.GetCollective(id)
}

func (s *MySQLStore) ListCollectives() ([]ChatSummary, error) {
	const q = `
SELECT
  c.id,
  c.created_at,
  COALESCE(MAX(child.updated_at), c.created_at) AS updated_at,
  COALESCE(
    NULLIF(JSON_UNQUOTE(JSON_EXTRACT(c.legacy_json, '$.Title')), ''),
    NULLIF(SUBSTRING(JSON_UNQUOTE(JSON_EXTRACT((
      SELECT i.legacy_json FROM interactions i
      WHERE i.user_id=c.user_id AND i.kind='incursion' AND i.collective_id=c.id
      ORDER BY i.created_at LIMIT 1
    ), '$.Prompt')), 1, 60), ''),
    '(no title)'
  ) AS title,
  COUNT(CASE WHEN child.kind='incursion' THEN 1 END) AS incursions,
  COUNT(CASE WHEN child.kind IN ('transmission','probe') THEN 1 END) AS messages
FROM interactions c
LEFT JOIN interactions child ON child.collective_id=c.id AND child.user_id=c.user_id
WHERE c.user_id=? AND c.kind='collective'
GROUP BY c.id
ORDER BY updated_at DESC`
	rows, err := s.db.Query(q, s.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatSummary
	for rows.Next() {
		var sum ChatSummary
		var created, updated string
		if err := rows.Scan(&sum.ID, &created, &updated, &sum.Title, &sum.Incursions, &sum.Messages); err != nil {
			return nil, err
		}
		sum.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		sum.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		out = append(out, sum)
	}
	return out, rows.Err()
}

func (s *MySQLStore) Close() error { return nil }
func (s *MySQLStore) Flush() error { return nil }

// UserStore manages the identity + roving-settings tables (server-owned, not
// per-user scoped — the server is the trust boundary for who is who).
type UserStore struct{ db *sql.DB }

func NewUserStore(db *sql.DB) *UserStore { return &UserStore{db: db} }

// UpsertLogin records a user (first login creates) and stamps last_login_at.
func (u *UserStore) UpsertLogin(userID, name string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := u.db.Exec(`
INSERT INTO users(id,name,created_at,last_login_at) VALUES(?,?,?,?)
AS new
ON DUPLICATE KEY UPDATE name=new.name, last_login_at=new.last_login_at`,
		userID, name, now, now)
	return err
}

// UserSettings is one user's roving settings document with its revision.
type UserSettings struct {
	Settings  map[string]any `json:"settings"`
	Revision  int64          `json:"revision"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// GetSettings returns the stored settings; sql.ErrNoRows when never set.
func (u *UserStore) GetSettings(userID string) (UserSettings, error) {
	var out UserSettings
	var raw []byte
	var stamp string
	err := u.db.QueryRow(`SELECT settings, revision, updated_at FROM user_settings WHERE user_id=?`, userID).Scan(&raw, &out.Revision, &stamp)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out.Settings); err != nil {
		return out, err
	}
	out.UpdatedAt, _ = time.Parse(time.RFC3339Nano, stamp)
	return out, nil
}

// ErrSettingsRevision is returned on a conflicting PUT (stale revision).
var ErrSettingsRevision = errors.New("settings revision conflict; reload before retrying")

// SetSettings stores the settings with optimistic concurrency: expected must
// match the stored revision (0 means "create").
func (u *UserStore) SetSettings(userID string, settings map[string]any, expected int64) (int64, error) {
	raw, err := json.Marshal(settings)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := u.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var current sql.NullInt64
	if err := tx.QueryRow(`SELECT revision FROM user_settings WHERE user_id=? FOR UPDATE`, userID).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if expected == 0 {
		if current.Valid {
			return 0, ErrSettingsRevision
		}
		if _, err := tx.Exec(`INSERT INTO user_settings(user_id,settings,revision,updated_at) VALUES(?,?,1,?)`, userID, raw, now); err != nil {
			return 0, err
		}
		return 1, tx.Commit()
	}
	if !current.Valid || current.Int64 != expected {
		return 0, ErrSettingsRevision
	}
	if _, err := tx.Exec(`UPDATE user_settings SET settings=?, revision=revision+1, updated_at=? WHERE user_id=?`, raw, now, userID); err != nil {
		return 0, err
	}
	return expected + 1, tx.Commit()
}
