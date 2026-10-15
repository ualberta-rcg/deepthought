package store

// MySQLStore is the server-side port of SQLiteStore: the same Borg-graph
// persistence (one polymorphic `interactions` row per Drone, canonical bodies,
// legacy_json as the rehydration source) behind the same ChatStore interface,
// but multi-user and networked. This package is SERVER-ONLY — the CLI never
// imports it (and so never links a SQL driver); clients reach this data over
// the server's HTTP API. Differences from the SQLite original, all deliberate:
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

	"deepthought-server/graph"
)

// Compile-time guarantee the MySQL store satisfies the same contracts.
var _ graph.ChatStore = (*MySQLStore)(nil)

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
	ensureUserColumns(db)
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
	  email VARCHAR(255) NOT NULL DEFAULT '',
	  oidc_sub VARCHAR(255) NOT NULL DEFAULT '',
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
	`CREATE TABLE IF NOT EXISTS summaries (
	  user_id VARCHAR(64) NOT NULL,
	  interaction_id VARCHAR(72) NOT NULL,
	  state VARCHAR(24) NOT NULL,
	  summarizer VARCHAR(64) NOT NULL,
	  text LONGTEXT NOT NULL,
	  tokens INT NOT NULL DEFAULT 0,
	  created_at VARCHAR(40) NOT NULL,
	  PRIMARY KEY (user_id, interaction_id, state, summarizer),
	  KEY idx_summaries_interaction (user_id, interaction_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS links (
	  user_id VARCHAR(64) NOT NULL,
	  src VARCHAR(72) NOT NULL,
	  dst VARCHAR(72) NOT NULL,
	  type VARCHAR(64) NOT NULL,
	  ordinal INT NOT NULL DEFAULT 0,
	  meta TEXT,
	  PRIMARY KEY (user_id, src, dst, type),
	  KEY idx_links_dst (user_id, dst, type)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS tool_executions (
	  user_id VARCHAR(64) NOT NULL,
	  id VARCHAR(72) NOT NULL,
	  session_id VARCHAR(72) NOT NULL DEFAULT '',
	  collective_id VARCHAR(72) NOT NULL DEFAULT '',
	  incursion_id VARCHAR(72) NOT NULL DEFAULT '',
	  transmission_id VARCHAR(72) NOT NULL DEFAULT '',
	  system_id VARCHAR(128) NOT NULL DEFAULT '',
	  cluster_name VARCHAR(64) NOT NULL DEFAULT '',
	  tool_name VARCHAR(64) NOT NULL DEFAULT '',
	  probe_type VARCHAR(64) NOT NULL DEFAULT '',
	  command TEXT,
	  target_path VARCHAR(1024) NOT NULL DEFAULT '',
	  arguments_json LONGTEXT,
	  response_text LONGTEXT,
	  response_summary TEXT,
	  is_error TINYINT(1) NOT NULL DEFAULT 0,
	  exit_code INT NOT NULL DEFAULT 0,
	  duration_ms BIGINT NOT NULL DEFAULT 0,
	  cwd VARCHAR(1024) NOT NULL DEFAULT '',
	  env_json TEXT,
	  slurm_job_id VARCHAR(64) NOT NULL DEFAULT '',
	  artifacts_json TEXT,
	  status VARCHAR(32) NOT NULL DEFAULT '',
	  decision VARCHAR(32) NOT NULL DEFAULT '',
	  started_at VARCHAR(40),
	  finished_at VARCHAR(40),
	  created_at VARCHAR(40) NOT NULL,
	  PRIMARY KEY (user_id, id),
	  KEY idx_tool_exec_coll (user_id, collective_id, created_at),
	  KEY idx_tool_exec_tool (user_id, tool_name, created_at),
	  KEY idx_tool_exec_job (user_id, slurm_job_id)
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
	`CREATE TABLE IF NOT EXISTS sessions (
	  token_hash BINARY(32) NOT NULL PRIMARY KEY,
	  user_id VARCHAR(64) NOT NULL,
	  name VARCHAR(128) NOT NULL,
	  created_at BIGINT NOT NULL,
	  last_seen BIGINT NOT NULL,
	  expires_at BIGINT NOT NULL,
	  KEY idx_sessions_user (user_id),
	  KEY idx_sessions_expires (expires_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS credential_salts (
	  user_id VARCHAR(64) NOT NULL PRIMARY KEY,
	  salt BINARY(32) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS credentials (
	  user_id VARCHAR(64) NOT NULL,
	  id CHAR(32) NOT NULL,
	  name VARCHAR(128) NOT NULL,
	  base_url VARCHAR(512) NOT NULL,
	  wire VARCHAR(32) NOT NULL,
	  kind VARCHAR(16) NOT NULL,
	  env_ref VARCHAR(256),
	  nonce VARBINARY(12),
	  ciphertext BLOB,
	  fingerprint CHAR(64) NOT NULL,
	  updated_at BIGINT NOT NULL,
	  PRIMARY KEY (user_id, id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS migrations (
	  name VARCHAR(128) NOT NULL PRIMARY KEY,
	  completed_at VARCHAR(40) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
}

func (s *MySQLStore) CreateCollective(req graph.SpawnCollectiveRequest) (*graph.Collective, error) {
	coll := graph.NewCollective(req)
	if err := s.SaveObject(coll); err != nil {
		return nil, err
	}
	return coll, nil
}

// ErrForeignID is returned when a save targets an interaction id that
// belongs to another user (ids are global; ownership is not transferable).
var ErrForeignID = errors.New("mysql store: interaction id belongs to another user")

func (s *MySQLStore) SaveObject(obj graph.Entity) error {
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		return s.saveEntityTx(context.Background(), tx, obj)
	})
}

// SaveCollective writes the whole graph in one transaction: a failure (or a
// foreign id anywhere in it) leaves no partially saved chat behind.
func (s *MySQLStore) SaveCollective(coll *graph.Collective) error {
	if coll == nil {
		return errors.New("mysql store: nil collective")
	}
	ctx := context.Background()
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, obj := range graph.LoadEntitiesWithPatterns(coll) {
			if err := s.saveEntityTx(ctx, tx, obj); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *MySQLStore) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *MySQLStore) saveEntityTx(ctx context.Context, tx *sql.Tx, obj graph.Entity) error {
	if obj == nil {
		return errors.New("mysql store: nil object")
	}
	if drone, ok := obj.(*graph.Drone); ok {
		return s.saveDroneTx(ctx, tx, drone, nil)
	}
	drone, err := graph.WrapEntity(obj)
	if err != nil {
		return err
	}
	legacy, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return s.saveDroneTx(ctx, tx, drone, legacy)
}

// UsageByModel aggregates token usage across THIS USER's graph.
func (s *MySQLStore) UsageByModel() (map[string]graph.Cost, error) {
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
	out := map[string]graph.Cost{}
	for rows.Next() {
		var mid sql.NullString
		var in, outTokens sql.NullInt64
		if err := rows.Scan(&mid, &in, &outTokens); err != nil {
			return nil, err
		}
		if mid.Valid && mid.String != "" {
			out[mid.String] = graph.Cost{InputTokens: int(in.Int64), OutputTokens: int(outTokens.Int64)}
		}
	}
	return out, rows.Err()
}

func (s *MySQLStore) SaveDrone(ctx context.Context, drone *graph.Drone, legacy []byte) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return s.saveDroneTx(ctx, tx, drone, legacy) })
}

func (s *MySQLStore) saveDroneTx(ctx context.Context, tx *sql.Tx, drone *graph.Drone, legacy []byte) error {
	if drone == nil || drone.ID == "" {
		return errors.New("mysql store: empty drone")
	}
	{
		raw, hash, err := graph.CanonicalBody(json.RawMessage(drone.Body))
		if err != nil {
			return err
		}
		drone.Body, drone.BodyHash = raw, hash
	}
	var owner string
	switch err := tx.QueryRowContext(ctx, `SELECT user_id FROM interactions WHERE id=? FOR UPDATE`, drone.ID).Scan(&owner); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("mysql store: owner check: %w", err)
	case owner != s.userID:
		return ErrForeignID
	}
	body, bodyRef, err := s.inlineOrStore(ctx, tx, drone.Body, drone.BodyHash)
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
	// IDs are app-generated (UUIDv7/newID prefixes) and the owner check above
	// refuses another user's id, so user_id is written once and never re-homed.
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
	nowStamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, tier := range []struct {
		state string
		text  string
	}{
		{"FULL", drone.Summaries.Full},
		{"DETAILED", drone.Summaries.Detailed},
		{"DIGEST", drone.Summaries.Condensed},
		{"LINE", drone.Summaries.Semantic},
		{"TOMBSTONE", drone.Summaries.Tombstone},
		{"EXEMPLAR", drone.Summaries.Exemplar},
	} {
		if tier.text == "" {
			continue
		}
		tokEst := (len(tier.text) + 3) / 4
		if _, err := tx.ExecContext(ctx, `
INSERT INTO summaries(user_id,interaction_id,state,summarizer,text,tokens,created_at)
VALUES(?,?,?,?,?,?,?)
AS new
ON DUPLICATE KEY UPDATE text=new.text, tokens=new.tokens`,
			s.userID, drone.ID, tier.state, "deterministic", tier.text, tokEst, nowStamp); err != nil {
			return err
		}
	}
	for ordinal, link := range drone.Links {
		meta, _ := json.Marshal(link.Metadata)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO links(user_id,src,dst,type,ordinal,meta) VALUES(?,?,?,?,?,?)
AS new
ON DUPLICATE KEY UPDATE ordinal=new.ordinal, meta=new.meta`,
			s.userID, drone.ID, link.TargetID, link.Relation, ordinal, string(meta)); err != nil {
			return err
		}
	}
	if drone.Kind == "probe" {
		rawProbe := legacy
		if len(rawProbe) == 0 {
			rawProbe = drone.Body
		}
		var p graph.Probe
		if json.Unmarshal(rawProbe, &p) == nil && p.ID != "" {
			rec := extractToolExecutionFromGraphProbe(&p, collectiveID)
			if _, err := tx.ExecContext(ctx, `
INSERT INTO tool_executions(
  user_id,id,session_id,collective_id,incursion_id,transmission_id,system_id,cluster_name,
  tool_name,probe_type,command,target_path,arguments_json,response_text,response_summary,
  is_error,exit_code,duration_ms,cwd,env_json,slurm_job_id,artifacts_json,
  status,decision,started_at,finished_at,created_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
AS new
ON DUPLICATE KEY UPDATE
  tool_name=new.tool_name, probe_type=new.probe_type, command=new.command,
  target_path=new.target_path, arguments_json=new.arguments_json,
  response_text=new.response_text, response_summary=new.response_summary,
  is_error=new.is_error, exit_code=new.exit_code, duration_ms=new.duration_ms,
  cwd=new.cwd, env_json=new.env_json, slurm_job_id=new.slurm_job_id,
  artifacts_json=new.artifacts_json, status=new.status, decision=new.decision,
  started_at=new.started_at, finished_at=new.finished_at`,
				s.userID, rec.ID, rec.SessionID, rec.CollectiveID, rec.IncursionID, rec.TransmissionID,
				rec.SystemID, rec.ClusterName, rec.ToolName, rec.ProbeType, rec.Command, rec.TargetPath,
				rec.ArgumentsJSON, rec.ResponseText, rec.ResponseSummary, rec.IsError, rec.ExitCode,
				rec.DurationMS, rec.CWD, rec.EnvJSON, rec.SlurmJobID, rec.ArtifactsJSON,
				rec.Status, rec.Decision, rec.StartedAt, rec.FinishedAt, rec.CreatedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

// inlineOrStore keeps small bodies inline and moves >1 MiB bodies to the
// content-addressed bodies table (the MySQL replacement for the filesystem
// spill). bodyRef is the hex sha256; reads re-verify the hash.
func (s *MySQLStore) inlineOrStore(ctx context.Context, tx *sql.Tx, body, hash []byte) ([]byte, any, error) {
	if len(body) <= graph.InlineBodyLimit {
		return body, nil, nil
	}
	ref := hex.EncodeToString(hash)
	_, err := tx.ExecContext(ctx, `INSERT IGNORE INTO bodies(sha256,body,created_at) VALUES(?,?,?)`, hash, body, time.Now().UTC().Format(time.RFC3339Nano))
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

func (s *MySQLStore) GetDrone(ctx context.Context, id string) (*graph.Drone, error) {
	row := s.db.QueryRowContext(ctx, `SELECT kind,session_id,collective_id,parent_id,body,body_ref,body_hash,state,pinned,poisoned,tokens,cost,producer,outcome,fail_class,sensitivity,use_count,last_used,created_at,updated_at FROM interactions WHERE id=? AND user_id=?`, id, s.userID)
	var d graph.Drone
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
	_, actualHash, hashErr := graph.CanonicalBody(body)
	if hashErr != nil {
		return nil, hashErr
	}
	d.BodyHash = actualHash
	_ = json.Unmarshal([]byte(tokens), &d.Tokens)
	_ = json.Unmarshal([]byte(cost), &d.Cost)
	_ = json.Unmarshal([]byte(producer), &d.Producer)
	if fail.Valid {
		fc := graph.FailureClass(fail.String)
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

func (s *MySQLStore) GetCollective(id string) (*graph.Collective, error) {
	rows, err := s.db.Query(`SELECT state, outcome, sensitivity, legacy_json FROM interactions WHERE user_id=? AND (id=? OR collective_id=?) AND legacy_json IS NOT NULL ORDER BY created_at`, s.userID, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entities []graph.Entity
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
		obj, err := graph.DecodeByKind(kind.Kind, raw)
		if err != nil {
			return nil, fmt.Errorf("history %s: %w", id, err)
		}
		e := obj.(graph.Entity)
		v := e.GetVinculum()
		if stateStr != "" {
			v.State = graph.State(stateStr)
		}
		if outcomeStr != "" {
			v.Outcome = graph.Outcome(outcomeStr)
		}
		v.Sensitivity = graph.Sensitivity(sensInt)
		entities = append(entities, e)
	}
	var coll *graph.Collective
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, entity := range entities {
		if value, ok := entity.(*graph.Collective); ok {
			coll = value
		}
	}
	if coll == nil {
		return nil, sql.ErrNoRows
	}
	coll.RebuildLinks(entities)
	return coll, nil
}

func (s *MySQLStore) Resume(id string) (*graph.Collective, error) {
	return s.GetCollective(id)
}

func (s *MySQLStore) ListCollectives() ([]graph.ChatSummary, error) {
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
	var out []graph.ChatSummary
	for rows.Next() {
		var sum graph.ChatSummary
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

func formatTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func extractToolExecutionFromGraphProbe(p *graph.Probe, collectiveID string) graph.ToolExecutionRecord {
	argsJSON := p.ArgumentsRaw
	if argsJSON == "" && len(p.Arguments) > 0 {
		raw, _ := json.Marshal(p.Arguments)
		argsJSON = string(raw)
	}
	if argsJSON == "" {
		argsJSON = "{}"
	}
	strArg := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := p.Arguments[k].(string); ok && v != "" {
				return v
			}
			if p.Metadata != nil {
				if v, ok := p.Metadata[k].(string); ok && v != "" {
					return v
				}
			}
		}
		return ""
	}
	exitCode := 0
	if p.Result.IsError {
		exitCode = 1
	}
	if p.Metadata != nil {
		if ec, ok := p.Metadata["exit_code"].(float64); ok {
			exitCode = int(ec)
		}
	}
	var durMS int64
	if !p.StartedAt.IsZero() && !p.FinishedAt.IsZero() && p.FinishedAt.After(p.StartedAt) {
		durMS = p.FinishedAt.Sub(p.StartedAt).Milliseconds()
	}
	created := time.Now().UTC().Format(time.RFC3339Nano)
	if !p.CreatedAt.IsZero() {
		created = p.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	var started, finished string
	if !p.StartedAt.IsZero() {
		started = p.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !p.FinishedAt.IsZero() {
		finished = p.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	summary := p.Result.Summary
	if summary == "" {
		summary = p.Result.Summaries.Semantic
	}
	return graph.ToolExecutionRecord{
		ID:              p.ID,
		SessionID:       p.SessionID,
		CollectiveID:    collectiveID,
		TransmissionID:  p.ParentID,
		SystemID:        strArg("system_id"),
		ClusterName:     strArg("cluster", "cluster_name"),
		ToolName:        p.Name,
		ProbeType:       p.ProbeType,
		Command:         strArg("command", "script", "action", "query"),
		TargetPath:      strArg("file_path", "path", "target_path", "script_path", "dir", "workdir"),
		ArgumentsJSON:   argsJSON,
		ResponseText:    p.Result.Content,
		ResponseSummary: summary,
		IsError:         p.Result.IsError,
		ExitCode:        exitCode,
		DurationMS:      durMS,
		CWD:             strArg("cwd", "workdir"),
		EnvJSON:         "{}",
		SlurmJobID:      strArg("job_id", "slurm_job_id"),
		ArtifactsJSON:   "[]",
		Status:          p.Status.String(),
		Decision:        fmt.Sprintf("%d", p.Decision),
		StartedAt:       started,
		FinishedAt:      finished,
		CreatedAt:       created,
	}
}

// ListToolExecutions queries structured tool execution rows for this user.
func (s *MySQLStore) ListToolExecutions(collectiveID, toolName string, limit int) ([]graph.ToolExecutionRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id,session_id,collective_id,incursion_id,transmission_id,system_id,cluster_name,
	  tool_name,probe_type,COALESCE(command,''),target_path,COALESCE(arguments_json,'{}'),
	  COALESCE(response_text,''),COALESCE(response_summary,''),is_error,exit_code,duration_ms,
	  cwd,COALESCE(env_json,'{}'),slurm_job_id,COALESCE(artifacts_json,'[]'),
	  status,decision,COALESCE(started_at,''),COALESCE(finished_at,''),created_at
	FROM tool_executions WHERE user_id=?`
	args := []any{s.userID}
	if collectiveID != "" {
		q += ` AND collective_id=?`
		args = append(args, collectiveID)
	}
	if toolName != "" {
		q += ` AND tool_name=?`
		args = append(args, toolName)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []graph.ToolExecutionRecord
	for rows.Next() {
		var r graph.ToolExecutionRecord
		var isErr int
		if err := rows.Scan(&r.ID, &r.SessionID, &r.CollectiveID, &r.IncursionID, &r.TransmissionID,
			&r.SystemID, &r.ClusterName, &r.ToolName, &r.ProbeType, &r.Command, &r.TargetPath,
			&r.ArgumentsJSON, &r.ResponseText, &r.ResponseSummary, &isErr, &r.ExitCode, &r.DurationMS,
			&r.CWD, &r.EnvJSON, &r.SlurmJobID, &r.ArtifactsJSON, &r.Status, &r.Decision,
			&r.StartedAt, &r.FinishedAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.IsError = isErr != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetSummaries returns all persisted summary tiers for an interaction ID.
func (s *MySQLStore) GetSummaries(interactionID string) ([]graph.SummaryRow, error) {
	rows, err := s.db.Query(`SELECT interaction_id,state,summarizer,text,tokens,created_at FROM summaries WHERE user_id=? AND interaction_id=? ORDER BY state`, s.userID, interactionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []graph.SummaryRow
	for rows.Next() {
		var r graph.SummaryRow
		if err := rows.Scan(&r.InteractionID, &r.State, &r.Summarizer, &r.Text, &r.Tokens, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UserStore manages the identity + roving-settings tables (server-owned, not
// per-user scoped — the server is the trust boundary for who is who).
type UserStore struct{ db *sql.DB }

func NewUserStore(db *sql.DB) *UserStore { return &UserStore{db: db} }

// UserRecord holds the stored identity for one user.
type UserRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email,omitempty"`
	OIDCSub     string `json:"oidc_sub,omitempty"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at"`
}

func ensureUserColumns(db *sql.DB) {
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN email VARCHAR(255) NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN oidc_sub VARCHAR(255) NOT NULL DEFAULT ''`)
}

// UpsertLogin records a user (first login creates) and stamps last_login_at.
func (u *UserStore) UpsertLogin(userID, name string) error {
	return u.UpsertLoginWithIdentity(userID, name, "", "")
}

// UpsertLoginWithIdentity records a user and optionally updates their OIDC email/subject.
func (u *UserStore) UpsertLoginWithIdentity(userID, name, email, oidcSub string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := u.db.Exec(`
INSERT INTO users(id,name,email,oidc_sub,created_at,last_login_at) VALUES(?,?,?,?,?,?)
AS new
ON DUPLICATE KEY UPDATE
 name=new.name,
 email=IF(new.email != '', new.email, users.email),
 oidc_sub=IF(new.oidc_sub != '', new.oidc_sub, users.oidc_sub),
 last_login_at=new.last_login_at`,
		userID, name, email, oidcSub, now, now)
	return err
}

// GetUser fetches a user's stored identity record.
func (u *UserStore) GetUser(userID string) (UserRecord, error) {
	var r UserRecord
	err := u.db.QueryRow(`SELECT id, name, email, oidc_sub, created_at, last_login_at FROM users WHERE id=?`, userID).
		Scan(&r.ID, &r.Name, &r.Email, &r.OIDCSub, &r.CreatedAt, &r.LastLoginAt)
	return r, err
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
