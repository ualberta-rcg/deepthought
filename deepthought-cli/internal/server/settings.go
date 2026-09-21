package server

// Server-side settings defaults: the "server" layer of the client's layered
// settings resolver (internal/config.ResolveLayers). Stored as JSON under the
// data dir; credential-bearing keys are rejected so a default can never push
// a secret down to clients (mirrors config.containsCredential).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func defaultsPath(dataDir string) string {
	return filepath.Join(dataDir, "settings-defaults.json")
}

// LoadDefaults reads the stored server defaults (empty map when absent).
func LoadDefaults(dataDir string) (map[string]any, error) {
	b, err := os.ReadFile(defaultsPath(dataDir))
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("settings defaults: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("settings defaults: %w", err)
	}
	return out, nil
}

// SaveDefaults atomically replaces the stored defaults.
func SaveDefaults(dataDir string, defaults map[string]any) error {
	if err := rejectCredentialKeys("", defaults); err != nil {
		return err
	}
	b, err := json.MarshalIndent(defaults, "", "  ")
	if err != nil {
		return fmt.Errorf("settings defaults: %w", err)
	}
	path := defaultsPath(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("settings defaults: %w", err)
	}
	return os.Rename(tmp, path)
}

// credentialKeyFragments names (key path segments or map keys) that mark a
// credential slot. A default carrying any of these is rejected outright.
var credentialKeyFragments = []string{
	"apikey", "api_key", "token", "secret", "password", "credential", "authorization",
}

// rejectCredentialKeys walks v recursively and fails on credential-named keys.
func rejectCredentialKeys(path string, v any) error {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			key := strings.ToLower(k)
			for _, frag := range credentialKeyFragments {
				if strings.Contains(key, frag) {
					return fmt.Errorf("server defaults cannot contain credential keys (%s)", filepath.Join(path, k))
				}
			}
			if err := rejectCredentialKeys(filepath.Join(path, k), sub); err != nil {
				return err
			}
		}
	case []any:
		for i, sub := range t {
			if err := rejectCredentialKeys(fmt.Sprintf("%s[%d]", path, i), sub); err != nil {
				return err
			}
		}
	}
	return nil
}
