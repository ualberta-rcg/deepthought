package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// DefaultsSource is an inactive seam for future server defaults. No client
// network dependency is introduced by the local layer resolver.
type DefaultsSource interface {
	Defaults() (map[string]any, error)
}

func fileMap(f File) map[string]any {
	raw, _ := json.Marshal(f)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func ResolveLayers(server, local, session map[string]any) (*Config, error) {
	merged := fileMap(Defaults().File)
	origins := map[string]string{}
	markOrigins(origins, "", merged, "built-in")
	if containsCredential(server) {
		return nil, fmt.Errorf("server defaults cannot contain credential values or references")
	}
	for _, layer := range []struct {
		name  string
		value map[string]any
	}{{"server", server}, {"local", local}, {"session", session}} {
		mergeLayer(merged, layer.value)
		markOrigins(origins, "", layer.value, layer.name)
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	for role, id := range f.Roles {
		if origins["roles."+role] == "built-in" {
			found := false
			for _, model := range f.Models {
				if model.ID == id {
					found = true
				}
			}
			if !found {
				delete(f.Roles, role)
			}
		}
	}
	cfg, err := Validate(f)
	if err != nil {
		return nil, err
	}
	cfg.Origins = origins
	return cfg, nil
}
func containsCredential(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			if (k == "api_key" || k == "password" || k == "token") && x != "" && x != nil {
				return true
			}
			if containsCredential(x) {
				return true
			}
		}
	case []any:
		for _, x := range v {
			if containsCredential(x) {
				return true
			}
		}
	}
	return false
}
func mergeLayer(dst, src map[string]any) {
	for key, value := range src {
		if value == nil {
			delete(dst, key)
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			base, _ := dst[key].(map[string]any)
			if base == nil {
				base = map[string]any{}
			}
			mergeLayer(base, nested)
			dst[key] = base
		} else {
			dst[key] = value
		}
	}
}
func markOrigins(out map[string]string, prefix string, values map[string]any, source string) {
	for key, value := range values {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		out[path] = source
		if nested, ok := value.(map[string]any); ok {
			markOrigins(out, path, nested, source)
		}
	}
}
func patchDelta(local, before, after map[string]any) {
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	for key := range keys {
		old, had := before[key]
		next, has := after[key]
		if had == has && reflect.DeepEqual(old, next) {
			continue
		}
		if !has {
			local[key] = nil
			continue
		}
		a, aOK := old.(map[string]any)
		b, bOK := next.(map[string]any)
		if aOK && bOK {
			patch, _ := local[key].(map[string]any)
			if patch == nil {
				patch = map[string]any{}
			}
			patchDelta(patch, a, b)
			local[key] = patch
		} else {
			local[key] = next
		}
	}
}

// SaveLocalPatch writes changes to the local override document, without
// materializing the merged defaults into that document.
func SaveLocalPatch(path string, raw []byte, before, after File) error {
	local := map[string]any{}
	if len(raw) == 0 {
		patchDelta(local, fileMap(Defaults().File), fileMap(before))
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &local); err != nil {
			return err
		}
		if _, legacy := local["provider"]; legacy {
			f, err := parse(raw)
			if err != nil {
				return err
			}
			local = fileMap(f)
		}
	}
	patchDelta(local, fileMap(before), fileMap(after))
	if _, err := ResolveLayers(nil, local, nil); err != nil {
		return err
	}
	data, err := json.MarshalIndent(local, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".overrides-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (c *Config) Source(path string) string {
	for path != "" {
		if source := c.Origins[path]; source != "" {
			return source
		}
		i := strings.LastIndexByte(path, '.')
		if i < 0 {
			break
		}
		path = path[:i]
	}
	return "built-in"
}
