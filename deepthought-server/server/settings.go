package server

// Server-side settings defaults: the "server" layer of the client's layered
// settings resolver (internal/config.ResolveLayers). Stored as JSON under the
// data dir; credential values are rejected so a default can never push a
// secret down to clients (mirrors config.containsCredential).

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

// credentialKeys are the setting names that hold secrets. Matching is exact
// (case-insensitive) so ordinary keys such as max_tokens pass; a credential
// key is allowed only when empty, which is how clients send their portable
// settings (keys blanked, bound locally).
var credentialKeys = map[string]bool{
	"api_key": true, "apikey": true, "password": true, "token": true,
	"access_token": true, "refresh_token": true, "secret": true,
	"client_secret": true, "credential": true, "credentials": true,
	"authorization": true,
}

// rejectCredentialKeys walks v recursively and fails on any non-empty value
// under a credential key: neither the defaults layer nor the synced user
// settings may carry a secret.
func rejectCredentialKeys(path string, v any) error {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			at := path + "/" + k
			if credentialKeys[strings.ToLower(k)] && !emptyValue(sub) {
				return fmt.Errorf("settings cannot contain credentials (%s); keys are bound on each machine", at)
			}
			if err := rejectCredentialKeys(at, sub); err != nil {
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

func emptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	}
	return false
}
