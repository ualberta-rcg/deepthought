package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"golang.org/x/sys/unix"
)

type mirrorState struct {
	Hash      string
	Revision  uint64
	Protected bool
}

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// A pre-mirror installation may still have its untouched migration source.
// Treat any unrecognized file as external input, never silently overwrite it.
func (s *LocalStore) initMirror(invalid bool) {
	var state mirrorState
	if s.ReadRecord("config-mirror", s.profile, &state) == nil {
		return
	}
	raw, err := readDiscoveryFile(s.path)
	if err == nil {
		state.Hash = digest(raw)
		original, e := readDiscoveryFile(s.path + ".before-database")
		state.Protected = invalid || e != nil || digest(original) != state.Hash
	} else if !os.IsNotExist(err) {
		state.Protected = true
	}
	_ = s.WriteRecord("config-mirror", s.profile, state)
}

// Saved returns persistent choices without environment or session overrides.
func (s *LocalStore) Saved() (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.savedLocked()
}
func (s *LocalStore) savedLocked() (*Config, error) {
	var raw []byte
	var revision uint64
	if err := s.db.QueryRow("SELECT body,revision FROM app_settings WHERE profile=?", s.profile).Scan(&raw, &revision); err != nil {
		return nil, err
	}
	var local map[string]any
	if err := json.Unmarshal(raw, &local); err != nil {
		return nil, err
	}
	// Fleet defaults remain a lower layer. User synchronization writes local settings.
	var defaults map[string]any
	_ = s.ReadRecord("fleet-defaults", s.profile, &defaults)
	cfg, err := ResolveLayers(defaults, local, nil)
	if err == nil {
		cfg.Revision, cfg.Local = revision, s
	}
	return cfg, err
}
func (s *LocalStore) MirrorNotice() string { s.mu.Lock(); defer s.mu.Unlock(); return s.mirrorNotice }
func (s *LocalStore) RepairMirror()        { s.mu.Lock(); defer s.mu.Unlock(); s.repairMirrorLocked() }
func (s *LocalStore) repairMirrorLocked() {
	if err := s.writeMirrorLocked(); err != nil {
		s.mirrorNotice = err.Error()
	} else {
		s.mirrorNotice = ""
	}
}
func (s *LocalStore) writeMirrorLocked() error {
	fail := func() error {
		return fmt.Errorf("Saved locally; config file update pending. Check its directory permissions and retry in Settings → Files.")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fail()
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fail()
	}
	defer lock.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return fail()
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	cfg, err := s.savedLocked()
	if err != nil {
		return fail()
	}
	wanted, err := json.MarshalIndent(cfg.File, "", "  ")
	if err != nil {
		return fail()
	}
	wanted = append(wanted, '\n')
	var state mirrorState
	if err = s.ReadRecord("config-mirror", s.profile, &state); err != nil {
		return fail()
	}
	raw, readErr := readDiscoveryFile(s.path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fail()
	}
	if readErr == nil && digest(raw) == digest(wanted) {
		return s.WriteRecord("config-mirror", s.profile, mirrorState{Hash: digest(wanted), Revision: cfg.Revision})
	}
	if state.Protected || (readErr == nil && digest(raw) != state.Hash) {
		return fmt.Errorf("Config file has external or invalid changes; preserved. Review import in Settings → Files.")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".config-*")
	if err != nil {
		return fail()
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(wanted); err != nil {
		f.Close()
		return fail()
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return fail()
	}
	if err = f.Close(); err != nil {
		return fail()
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return fail()
	}
	return s.WriteRecord("config-mirror", s.profile, mirrorState{Hash: digest(wanted), Revision: cfg.Revision})
}

// ImportFile acknowledges exactly the bytes validated; concurrent external edits
// still prevent replacement. Invalid input leaves both local copies intact.
func (s *LocalStore) ImportChanges() ([]string, error) {
	raw, err := readDiscoveryFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("could not read config file")
	}
	f, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid config file; original preserved")
	}
	if _, err = Validate(f); err != nil {
		return nil, fmt.Errorf("invalid settings; review the file before importing")
	}
	saved, err := s.Saved()
	if err != nil {
		return nil, err
	}
	a, b := fileMap(saved.File), fileMap(f)
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var changed []string
	for k := range keys {
		if !reflect.DeepEqual(a[k], b[k]) {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return changed, nil // Field names only; never expose credential values.
}

func (s *LocalStore) ImportFile() (*Config, error) {
	raw, err := readDiscoveryFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("could not read config file")
	}
	f, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid config file; original preserved")
	}
	if _, err = Validate(f); err != nil {
		return nil, err
	}
	before, err := s.Saved()
	if err != nil {
		return nil, err
	}
	f.Revision = before.Revision
	// A failed DB save still leaves the file and its contents intact.
	cfg, err := s.Save(before.File, f)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.WriteRecord("config-mirror", s.profile, mirrorState{Hash: digest(raw)}); err != nil {
		return cfg, err
	}
	s.repairMirrorLocked()
	s.session = map[string]any{}
	return s.loadLocked()
}
func (s *LocalStore) ProfileKey() string { return s.profile }
