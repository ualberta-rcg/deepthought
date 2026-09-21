package history

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const InlineBodyLimit = 1 << 20

// Compile-time guarantee that SQLiteStore is a ChatStore (Store + Resume +
// ListCollectives). FileStore satisfies it too; MemStore does not.
var _ ChatStore = (*SQLiteStore)(nil)

// SQLiteStore is the authoritative local store. Large bodies spill to a
// content-addressed directory so one tool result cannot become one huge DB
// transaction.
type SQLiteStore struct {
	db      *sql.DB
	bodyDir string
}

func NewSQLiteStore(path, bodyDir string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if bodyDir == "" {
		bodyDir = filepath.Join(filepath.Dir(path), "bodies")
	}
	if err := os.MkdirAll(bodyDir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &SQLiteStore{db: db, bodyDir: bodyDir}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateIdentity(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) init() error {
	_, err := s.db.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS interactions (
  id TEXT NOT NULL PRIMARY KEY,
  kind TEXT NOT NULL,
  session_id TEXT NOT NULL,
  collective_id TEXT,
  parent_id TEXT,
  body BLOB,
  body_ref TEXT,
  body_hash BLOB NOT NULL,
  state TEXT NOT NULL,
  pinned INTEGER NOT NULL DEFAULT 0,
  poisoned INTEGER NOT NULL DEFAULT 0,
  tokens TEXT NOT NULL DEFAULT '{}',
  cost TEXT NOT NULL DEFAULT '{}',
  producer TEXT,
  outcome TEXT,
  fail_class TEXT,
  sensitivity INTEGER NOT NULL DEFAULT 0,
  embedding BLOB,
  use_count INTEGER NOT NULL DEFAULT 0,
  last_used TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT,
  legacy_json BLOB
);
CREATE INDEX IF NOT EXISTS interactions_session_created ON interactions(session_id, created_at);
CREATE INDEX IF NOT EXISTS interactions_collective_kind_state ON interactions(collective_id, kind, state);
CREATE TABLE IF NOT EXISTS links (
  src TEXT NOT NULL,
  dst TEXT NOT NULL,
  type TEXT NOT NULL,
  ordinal INTEGER,
  meta TEXT,
  PRIMARY KEY (src, dst, type)
);
CREATE INDEX IF NOT EXISTS links_dst_type ON links(dst, type);
CREATE TABLE IF NOT EXISTS summaries (
  interaction_id TEXT NOT NULL,
  state TEXT NOT NULL,
  summarizer TEXT NOT NULL,
  text TEXT NOT NULL,
  tokens INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (interaction_id, state, summarizer)
);
CREATE TABLE IF NOT EXISTS migrations (
  name TEXT NOT NULL PRIMARY KEY,
  completed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS body_versions (
 interaction_id TEXT NOT NULL, body_hash BLOB NOT NULL, body BLOB, body_ref TEXT,
 created_at TEXT NOT NULL, PRIMARY KEY(interaction_id, body_hash)
);
CREATE TABLE IF NOT EXISTS drone_metadata (
 interaction_id TEXT PRIMARY KEY, summaries TEXT NOT NULL, links TEXT NOT NULL
);`)
	return err
}

// MigrateLegacyChats imports the existing JSONL collectives exactly once.
func (s *SQLiteStore) MigrateLegacyChats(dir string) error {
	var marker string
	err := s.db.QueryRow(`SELECT name FROM migrations WHERE name='jsonl_to_drones_v2'`).Scan(&marker)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	summaries, err := ListCollectives(dir)
	if err != nil {
		return err
	}
	for _, summary := range summaries {
		var exists int
		if err := s.db.QueryRow(`SELECT count(*) FROM interactions WHERE id=? AND legacy_json IS NOT NULL`, summary.ID).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		} // never overwrite chats updated since import
		coll, err := NewFileStore(dir).GetCollective(summary.ID)
		if err != nil {
			return fmt.Errorf("migrate %s: %w", summary.ID, err)
		}
		if err := s.SaveCollective(coll); err != nil {
			return fmt.Errorf("migrate %s: %w", summary.ID, err)
		}
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO migrations(name,completed_at) VALUES('jsonl_to_drones_v2',?)`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) Close() error { return s.db.Close() }
func (s *SQLiteStore) Flush() error { return nil }

// Export writes every collective to dir as <id>.jsonl, one entity per line in
// the same format FileStore writes — so an export is readable by FileStore
// (and re-importable via MigrateLegacyChats). JSONL is export-only now that
// SQLite is authoritative; this is the backup/interop path.
func (s *SQLiteStore) Export(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sums, err := s.ListCollectives()
	if err != nil {
		return err
	}
	for _, sum := range sums {
		coll, err := s.GetCollective(sum.ID)
		if err != nil {
			return fmt.Errorf("export %s: %w", sum.ID, err)
		}
		f, err := os.OpenFile(filepath.Join(dir, sum.ID+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		for _, obj := range loadEntitiesWithPatterns(coll) {
			raw, err := json.Marshal(obj)
			if err != nil {
				f.Close()
				return err
			}
			if _, err := f.Write(append(raw, '\n')); err != nil {
				f.Close()
				return err
			}
		}
		f.Close()
	}
	return nil
}

func (s *SQLiteStore) CreateCollective(req SpawnCollectiveRequest) (*Collective, error) {
	coll := NewCollective(req)
	if err := s.SaveObject(coll); err != nil {
		return nil, err
	}
	return coll, nil
}

func (s *SQLiteStore) SaveObject(obj Entity) error {
	if obj == nil {
		return errors.New("sqlite store: nil object")
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

func (s *SQLiteStore) SaveCollective(coll *Collective) error {
	if coll == nil {
		return errors.New("sqlite store: nil collective")
	}
	for _, obj := range loadEntitiesWithPatterns(coll) {
		if err := s.SaveObject(obj); err != nil {
			return err
		}
	}
	return nil
}

func loadEntitiesWithPatterns(coll *Collective) []Entity {
	out := loadEntities(coll)
	for _, inc := range coll.Incursions {
		for _, tx := range inc.Transmissions {
			for _, probe := range tx.Probes {
				for _, pattern := range probe.Patterns {
					out = append(out, pattern)
				}
			}
		}
	}
	return out
}

func legacyEntityDrone(obj Entity) (*Drone, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	drone, err := NewDrone(obj.ObjectKind(), obj.GetVinculum().SessionID, json.RawMessage(raw))
	if err != nil {
		return nil, err
	}
	drone.Vinculum = *obj.GetVinculum()
	// Enrich the Drone from the graph node's own metadata. Outcome is derived
	// from the lifecycle status where possible; the rest are caller-set.
	v := obj.GetVinculum()
	drone.State = stateOrDefault(v.State, StateFull)
	drone.Outcome = outcomeFromEntity(obj)
	if v.Producer != nil {
		drone.Producer = *v.Producer
	}
	drone.Cost = v.Cost
	drone.Sensitivity = v.Sensitivity
	return drone, nil
}

// Compile-time guarantee that SQLiteStore is a UsageReporter (Status dashboard).
var _ UsageReporter = (*SQLiteStore)(nil)

// UsageByModel aggregates token usage across all history, keyed by model id
// (from the producer JSON on model-driven transmissions/incursions). Used by the
// Status dashboard's per-model token view. Models with no recorded usage are
// absent from the map.
func (s *SQLiteStore) UsageByModel() (map[string]Cost, error) {
	const q = `SELECT
  NULLIF(json_extract(producer,'$.model_id'),'') AS mid,
  SUM(COALESCE(json_extract(cost,'$.input_tokens'),0)),
  SUM(COALESCE(json_extract(cost,'$.output_tokens'),0))
FROM interactions
WHERE kind IN ('transmission','incursion') AND json_valid(producer)
GROUP BY mid`
	rows, err := s.db.Query(q)
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

// stateOrDefault returns def when s is empty (the default residency).
func stateOrDefault(s, def State) State {
	if s == "" {
		return def
	}
	return s
}

// outcomeFromEntity maps a graph node's lifecycle status to an Outcome. Probes
// and Incursions carry a status; other kinds fall back to whatever Outcome the
// node's Vinculum already carries.
func outcomeFromEntity(obj Entity) Outcome {
	switch e := obj.(type) {
	case *Probe:
		switch e.Status {
		case ProbeCompleted:
			return OutcomeSuccess
		case ProbeFailed:
			return OutcomeFailure
		}
	case *Incursion:
		switch e.Status {
		case IncursionCompleted:
			return OutcomeSuccess
		case IncursionFailed:
			return OutcomeFailure
		case IncursionInterrupted:
			return OutcomePartial
		}
	}
	return obj.GetVinculum().Outcome
}

func (s *SQLiteStore) SaveDrone(ctx context.Context, drone *Drone, legacy []byte) error {
	if drone == nil || drone.ID == "" {
		return errors.New("sqlite store: empty drone")
	}
	{
		raw, hash, err := canonicalBody(json.RawMessage(drone.Body))
		if err != nil {
			return err
		}
		drone.Body, drone.BodyHash = raw, hash
	}
	body, bodyRef, err := s.inlineOrSpill(drone.Body, drone.BodyHash)
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
	_, err = tx.ExecContext(ctx, `
INSERT INTO interactions
(id,kind,session_id,collective_id,parent_id,body,body_ref,body_hash,state,pinned,poisoned,tokens,cost,producer,outcome,fail_class,sensitivity,use_count,last_used,created_at,updated_at,legacy_json)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
 body=excluded.body, body_ref=excluded.body_ref, body_hash=excluded.body_hash, state=excluded.state,
 pinned=excluded.pinned, poisoned=excluded.poisoned, tokens=excluded.tokens,
 cost=excluded.cost, producer=excluded.producer, outcome=excluded.outcome,
 fail_class=excluded.fail_class, sensitivity=excluded.sensitivity,
 use_count=excluded.use_count, last_used=excluded.last_used,
 updated_at=excluded.updated_at, legacy_json=COALESCE(excluded.legacy_json, interactions.legacy_json)`,
		drone.ID, drone.Kind, drone.SessionID, collectiveID, drone.ParentID,
		body, bodyRef, drone.BodyHash, drone.State, drone.Pinned, drone.Poisoned,
		string(tokens), string(cost), string(producer), drone.Outcome, fail,
		drone.Sensitivity, drone.UseCount, formatTimePtr(drone.LastUsed),
		formatTime(drone.CreatedAt), formatTime(drone.UpdatedAt), legacy)
	if err != nil {
		return fmt.Errorf("sqlite store: save interaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO body_versions(interaction_id,body_hash,body,body_ref,created_at) VALUES(?,?,?,?,?)`, drone.ID, drone.BodyHash, body, bodyRef, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	summaryJSON, err := json.Marshal(drone.Summaries)
	if err != nil {
		return err
	}
	linksJSON, err := json.Marshal(drone.Links)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO drone_metadata(interaction_id,summaries,links) VALUES(?,?,?) ON CONFLICT(interaction_id) DO UPDATE SET summaries=excluded.summaries,links=excluded.links`, drone.ID, string(summaryJSON), string(linksJSON)); err != nil {
		return err
	}
	for ordinal, link := range drone.Links {
		meta, _ := json.Marshal(link.Metadata)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO links(src,dst,type,ordinal,meta) VALUES(?,?,?,?,?)
ON CONFLICT(src,dst,type) DO UPDATE SET ordinal=excluded.ordinal,meta=excluded.meta`,
			drone.ID, link.TargetID, link.Relation, ordinal, string(meta)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) inlineOrSpill(body, hash []byte) ([]byte, any, error) {
	if len(body) <= InlineBodyLimit {
		return body, nil, nil
	}
	name := hex.EncodeToString(hash) + ".json"
	path := filepath.Join(s.bodyDir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.CreateTemp(s.bodyDir, ".body-*")
		if err != nil {
			return nil, nil, err
		}
		tmp := f.Name()
		defer os.Remove(tmp)
		if _, err := f.Write(body); err != nil {
			f.Close()
			return nil, nil, err
		}
		if err := f.Close(); err != nil {
			return nil, nil, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return nil, nil, err
		}
	}
	return nil, name, nil
}

func (s *SQLiteStore) GetDrone(ctx context.Context, id string) (*Drone, error) {
	row := s.db.QueryRowContext(ctx, `SELECT kind,session_id,collective_id,parent_id,body,body_ref,body_hash,state,pinned,poisoned,tokens,cost,producer,outcome,fail_class,sensitivity,use_count,last_used,created_at,updated_at FROM interactions WHERE id=?`, id)
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
		body, err = os.ReadFile(filepath.Join(s.bodyDir, bodyRef.String))
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

func (s *SQLiteStore) GetCollective(id string) (*Collective, error) {
	// Pull legacy_json (the canonical body) plus the residency state, outcome,
	// and sensitivity columns so the rehydrated graph sees each node's own
	// metadata (the bridge that lets MessagesByState render a demoted probe
	// shorter, and lets the model layer read its own provenance).
	rows, err := s.db.Query(`SELECT state, outcome, sensitivity, legacy_json FROM interactions WHERE (id=? OR collective_id=?) AND legacy_json IS NOT NULL ORDER BY created_at`, id, id)
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
		if err == nil {
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

// Resume loads a collective for continued appending. SQLiteStore is stateless
// wrt the "active" collective — SaveObject routes by each entity's own
// CollectiveID, and one shared store serves every SSH session concurrently — so
// Resume is just GetCollective. The method exists to satisfy ChatStore and to
// mirror FileStore.Resume's contract.
func (s *SQLiteStore) Resume(id string) (*Collective, error) {
	return s.GetCollective(id)
}

// ListCollectives enumerates chats (kind='collective') with their title, turn
// counts, and timestamps, most-recently-touched first — the Continue screen's
// data source. Title comes from the collective's stored Title, falling back to
// the first incursion's Prompt, then to "(no title)".
func (s *SQLiteStore) ListCollectives() ([]ChatSummary, error) {
	const q = `
SELECT
  c.id,
  c.created_at,
  COALESCE(MAX(child.updated_at), c.created_at) AS updated_at,
  COALESCE(
    NULLIF(json_extract(c.legacy_json, '$.Title'), ''),
    NULLIF(substr(json_extract((
      SELECT i.legacy_json FROM interactions i
      WHERE i.kind='incursion' AND i.collective_id=c.id
      ORDER BY i.created_at LIMIT 1
    ), '$.Prompt'), 1, 60), ''),
    '(no title)'
  ) AS title,
  COUNT(CASE WHEN child.kind='incursion' THEN 1 END) AS incursions,
  COUNT(CASE WHEN child.kind IN ('transmission','probe') THEN 1 END) AS messages
FROM interactions c
LEFT JOIN interactions child ON child.collective_id=c.id
WHERE c.kind='collective'
GROUP BY c.id
ORDER BY updated_at DESC`
	rows, err := s.db.Query(q)
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
