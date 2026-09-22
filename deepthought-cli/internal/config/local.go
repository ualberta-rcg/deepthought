package config

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"deepthought-cli/internal/credential"
	_ "modernc.org/sqlite"
)

// LocalStore shares history.db, but uses independent versioned settings and
// inventory tables. A settings profile is scoped to its original config path.
type LocalStore struct {
	db                *sql.DB
	path, profile     string
	mu                sync.Mutex
	explicit, session map[string]any
}

func OpenLocal(path string, explicit bool) (*Config, error) {
	dir, err := DataDir()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "history.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS app_settings(profile TEXT PRIMARY KEY, revision INTEGER NOT NULL, body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS app_inventory(kind TEXT NOT NULL,id TEXT NOT NULL,body BLOB NOT NULL,updated TEXT NOT NULL,PRIMARY KEY(kind,id));`)
	if err != nil {
		db.Close()
		return nil, err
	}
	abs, _ := filepath.Abs(path)
	s := &LocalStore{db: db, path: path, profile: abs, session: map[string]any{}}
	notice := ""
	if err := LoadSecrets(path); err != nil {
		notice = "Credential file unavailable; reopen provider setup to repair it."
	}
	var existing int
	err = db.QueryRow("SELECT revision FROM app_settings WHERE profile=?", s.profile).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		f := Defaults().File
		importedFile := false
		raw, readErr := os.ReadFile(path)
		if readErr == nil {
			imported, parseErr := Load(path)
			if parseErr != nil {
				notice = "Settings file is invalid; original preserved. Use Settings to repair or import it."
			} else {
				f = imported.File
				importedFile = true
				backup := path + ".before-database"
				out, e := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if e == nil {
					_, e = out.Write(raw)
					closeErr := out.Close()
					if e == nil {
						e = closeErr
					}
				}
				if e != nil && !os.IsExist(e) {
					db.Close()
					return nil, fmt.Errorf("preserve original settings: %w", e)
				}
			}
		} else if !os.IsNotExist(readErr) {
			notice = "Settings file could not be read; original preserved."
		}
		// Import the old separate key map without discarding settings on failure.
		if b, e := os.ReadFile(filepath.Join(filepath.Dir(path), "keybindings.json")); e == nil {
			var old map[string]map[string]string
			if json.Unmarshal(b, &old) == nil {
				f.Keybindings = old["global"]
			}
		}
		if err = s.separateSecrets(&f); err != nil {
			db.Close()
			return nil, err
		}
		body, _ := json.Marshal(f)
		if !importedFile {
			body = []byte(`{}`)
		}
		_, err = db.Exec("INSERT OR IGNORE INTO app_settings(profile,revision,body) VALUES(?,1,?)", s.profile, body)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	if explicit {
		if f, e := Load(path); e == nil {
			sf := f.File
			if e = s.separateSecrets(&sf); e != nil {
				db.Close()
				return nil, e
			}
			raw, readErr := os.ReadFile(path)
			var selected map[string]any
			if readErr == nil && json.Unmarshal(raw, &selected) == nil {
				if _, legacy := selected["provider"]; legacy {
					s.explicit = fileMap(sf) // v1 normalization changes field names
				} else {
					s.explicit = explicitFields(selected, fileMap(sf))
				}
			}
		} else {
			notice = "Explicit configuration is invalid; using saved settings. Original preserved."
		}
	}
	cfg, err := s.Load()
	if err != nil {
		db.Close()
		return nil, err
	}
	if notice != "" {
		cfg.StartupNotice = notice
	}
	return cfg, nil
}

func (s *LocalStore) Close() error { return s.db.Close() }

// Override only keys actually present in an explicit document, using normalized
// values so credentials stay references and nested defaults do not mask saves.
func explicitFields(selected, normalized map[string]any) map[string]any {
	out := map[string]any{}
	for k, value := range selected {
		n, ok := normalized[k]
		if !ok {
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			if nm, ok := n.(map[string]any); ok {
				out[k] = explicitFields(nested, nm)
				continue
			}
		}
		out[k] = n
	}
	return out
}

func (s *LocalStore) SetServerDefaults(defaults, remote map[string]any) error {
	clean := func(m map[string]any) (map[string]any, error) {
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		var f File
		if err = json.Unmarshal(b, &f); err != nil {
			return nil, err
		}
		return fileMap(Portable(f)), nil
	}
	// Never allow a server to install credential references or local settings files.
	d, err := clean(defaults)
	if err != nil {
		return err
	}
	r, err := clean(remote)
	if err != nil {
		return err
	}
	mergeLayer(d, r)
	if _, err := ResolveLayers(d, nil, nil); err != nil {
		return err
	}
	return s.WriteRecord("server-defaults", s.profile, d)
}
func (s *LocalStore) Load() (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}
func (s *LocalStore) loadLocked() (*Config, error) {
	var raw []byte
	var rev uint64
	if err := s.db.QueryRow("SELECT revision,body FROM app_settings WHERE profile=?", s.profile).Scan(&rev, &raw); err != nil {
		return nil, err
	}
	var local map[string]any
	if err := json.Unmarshal(raw, &local); err != nil {
		return nil, err
	}
	var server map[string]any
	_ = s.ReadRecord("server-defaults", s.profile, &server)
	cfg, err := ResolveLayers(server, local, nil)
	if err != nil {
		return nil, err
	}
	merged := fileMap(cfg.File)
	// Only documented application overrides apply; credential discovery never
	// changes provider choice through an unrelated environment variable.
	env := map[string]any{}
	if v := os.Getenv("DEEPTHOUGHT_EFFORT"); v != "" {
		env["effort"] = v
	}
	mergeLayer(merged, env)
	markOrigins(cfg.Origins, "", env, "environment")
	mergeLayer(merged, s.explicit)
	markOrigins(cfg.Origins, "", s.explicit, "explicit file")
	mergeLayer(merged, s.session)
	markOrigins(cfg.Origins, "", s.session, "session")
	b, _ := json.Marshal(merged)
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	validated, err := Validate(f)
	if err != nil {
		cfg.Local, cfg.Revision = s, rev
		cfg.StartupNotice = "External settings overrides are invalid; using saved settings. Correct the override in Settings or its source."
		return cfg, nil
	}
	validated.Local, validated.Origins, validated.Revision = s, cfg.Origins, rev
	return validated, nil
}

func (s *LocalStore) Save(before, after File) (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := Validate(after); err != nil {
		return nil, err
	}
	if err := s.separateSecrets(&after); err != nil {
		return nil, err
	}
	var raw []byte
	var rev uint64
	if err := s.db.QueryRow("SELECT revision,body FROM app_settings WHERE profile=?", s.profile).Scan(&rev, &raw); err != nil {
		return nil, err
	}
	if after.Revision != 0 && after.Revision != rev {
		return nil, fmt.Errorf("settings changed in another session; reload before saving")
	}
	var local map[string]any
	if err := json.Unmarshal(raw, &local); err != nil {
		return nil, err
	}
	patchDelta(local, fileMap(before), fileMap(after))
	body, _ := json.Marshal(local)
	result, err := s.db.Exec("UPDATE app_settings SET body=?,revision=revision+1 WHERE profile=? AND revision=?", body, s.profile, rev)
	if err != nil {
		return nil, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return nil, fmt.Errorf("settings changed; reload before saving")
	}
	// An edit of an externally overridden value is also a session override.
	patchDelta(s.session, fileMap(before), fileMap(after))
	return s.loadLocked()
}

func (s *LocalStore) ReadRecord(kind, id string, dst any) error {
	var raw []byte
	if err := s.db.QueryRow("SELECT body FROM app_inventory WHERE kind=? AND id=?", kind, id).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}
func (s *LocalStore) WriteRecord(kind, id string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO app_inventory(kind,id,body,updated) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET body=excluded.body,updated=excluded.updated", kind, id, body, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *LocalStore) Records(kind string) ([]json.RawMessage, error) {
	rows, err := s.db.Query("SELECT body FROM app_inventory WHERE kind=? ORDER BY updated DESC LIMIT 256", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *LocalStore) separateSecrets(f *File) error {
	values := map[string]string{}
	replace := func(label string, value *string) {
		if *value == "" || strings.HasPrefix(*value, "$") || strings.HasPrefix(*value, "secret:") {
			return
		}
		sum := sha256.Sum256([]byte(s.profile + ":" + label + ":" + *value))
		id := "DT_" + hex.EncodeToString(sum[:16])
		values[id] = *value
		credential.Register("secret:"+id, *value)
		*value = "secret:" + id
	}
	for i := range f.Providers {
		replace("provider:"+f.Providers[i].Name, &f.Providers[i].APIKey)
	}
	if f.Server != nil {
		replace("server", &f.Server.Password)
	}
	if len(values) == 0 {
		return nil
	}
	return writeSecrets(filepath.Join(filepath.Dir(s.path), "secrets.env"), values)
}

// Portable excludes credentials and machine-local bindings from server sync.
func Portable(f File) File {
	raw, _ := json.Marshal(f)
	var out File
	_ = json.Unmarshal(raw, &out)
	for i := range out.Providers {
		out.Providers[i].APIKey = ""
		out.Providers[i].Manifest = ""
	}
	out.Server = nil
	return out
}
