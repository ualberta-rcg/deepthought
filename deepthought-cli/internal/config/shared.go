package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// SharedDocument is an allowlist: local credentials, executable hooks, tool
// manifests, permissions, personal notes and server login never cross this seam.
type SharedDocument map[string]any

var sharedKeys = []string{"providers", "models", "roles", "routes", "thinking", "effort", "max_tokens", "temperature", "language", "appearance", "keybindings"}

func Shared(f File) SharedDocument {
	f = Portable(f)
	raw := fileMap(f)
	out := SharedDocument{}
	for _, k := range sharedKeys {
		if v, ok := raw[k]; ok {
			out[k] = v
		}
	}
	if providers, ok := out["providers"].([]any); ok {
		var kept []any
		for _, v := range providers {
			p, _ := v.(map[string]any)
			if p["kind"] == "tool_server" {
				continue
			}
			delete(p, "api_key")
			delete(p, "manifest")
			kept = append(kept, p)
		}
		out["providers"] = kept
	}
	return out
}
func ReadShared(raw map[string]any) (SharedDocument, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var f File
	if err = json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("unsupported server settings")
	}
	doc := Shared(f)
	// Preserve omitted fields in sparse server documents.
	for k := range doc {
		if _, ok := raw[k]; !ok {
			delete(doc, k)
		}
	}
	return doc, nil
}

// ApplyShared preserves private fields, but a changed endpoint cannot inherit a
// key chosen for another origin. Missing credentials are a recoverable UI state.
func ApplyShared(local File, doc SharedDocument) (File, error) {
	m := fileMap(local)
	for _, k := range sharedKeys {
		delete(m, k)
	}
	for k, v := range doc {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return File{}, err
	}
	var out File
	if err = json.Unmarshal(raw, &out); err != nil {
		return File{}, err
	}
	for i := range out.Providers {
		out.Providers[i].APIKey = ""
		out.Providers[i].Manifest = ""
		for _, p := range local.Providers {
			if p.Name == out.Providers[i].Name && strings.TrimRight(p.BaseURL, "/") == strings.TrimRight(out.Providers[i].BaseURL, "/") && p.Wire == out.Providers[i].Wire {
				out.Providers[i].APIKey = p.APIKey
			}
		}
	}
	for _, p := range local.Providers {
		if p.Kind == "tool_server" {
			out.Providers = append(out.Providers, p)
		}
	}
	out.Revision = local.Revision
	if _, err = Validate(out); err != nil {
		return File{}, fmt.Errorf("merged settings need review: %w", err)
	}
	return out, nil
}

type SyncConflict struct {
	Path                        string
	Local, Remote               any
	LocalPresent, RemotePresent bool
}
type SyncChoice struct{ Fingerprint, Side string }

func (c SyncConflict) Fingerprint() string { b, _ := json.Marshal(c); return digest(b) }

// Three-way merge, with named providers/models rather than whole-array replacement.
// Missing values are tombstones only when present in the common baseline.
func MergeShared(base, local, remote SharedDocument, choices map[string]SyncChoice) (SharedDocument, []SyncConflict) {
	b, l, r := keyed(base), keyed(local), keyed(remote)
	var conflicts []SyncConflict
	var merge func(string, any, bool, any, bool, any, bool) (any, bool)
	same := func(a any, ap bool, b any, bp bool) bool { return ap == bp && reflect.DeepEqual(a, b) }
	merge = func(path string, b any, bp bool, l any, lp bool, r any, rp bool) (any, bool) {
		if same(l, lp, r, rp) {
			return l, lp
		}
		if same(l, lp, b, bp) {
			return r, rp
		}
		if same(r, rp, b, bp) {
			return l, lp
		}
		lm, lok := l.(map[string]any)
		rm, rok := r.(map[string]any)
		bm, bok := b.(map[string]any)
		if lok && rok && (!bp || bok) {
			out := map[string]any{}
			keys := map[string]bool{}
			for k := range bm {
				keys[k] = true
			}
			for k := range lm {
				keys[k] = true
			}
			for k := range rm {
				keys[k] = true
			}
			names := []string{}
			for k := range keys {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				bv, bp := bm[k]
				lv, lp := lm[k]
				rv, rp := rm[k]
				v, ok := merge(path+"/"+strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1"), bv, bp, lv, lp, rv, rp)
				if ok {
					out[k] = v
				}
			}
			return out, true
		}
		c := SyncConflict{path, l, r, lp, rp}
		if choice, ok := choices[path]; ok && choice.Fingerprint == c.Fingerprint() {
			if choice.Side == "remote" {
				return r, rp
			}
			if choice.Side == "local" {
				return l, lp
			}
		}
		conflicts = append(conflicts, c)
		return l, lp
	}
	v, _ := merge("", b, true, l, true, r, true)
	return unkeyed(v.(map[string]any)), conflicts
}
func keyed(doc SharedDocument) map[string]any {
	raw, _ := json.Marshal(doc)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	for _, pair := range []struct{ key, id string }{{"providers", "name"}, {"models", "id"}} {
		items := map[string]any{}
		if list, ok := out[pair.key].([]any); ok {
			for _, v := range list {
				if item, ok := v.(map[string]any); ok {
					if id, ok := item[pair.id].(string); ok {
						items[id] = item
					}
				}
			}
		}
		out[pair.key] = items
	}
	return out
}
func unkeyed(doc map[string]any) SharedDocument {
	for _, key := range []string{"providers", "models"} {
		items, _ := doc[key].(map[string]any)
		ids := []string{}
		for id := range items {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		list := []any{}
		for _, id := range ids {
			list = append(list, items[id])
		}
		doc[key] = list
	}
	return SharedDocument(doc)
}
func SameShared(a, b SharedDocument) bool { return reflect.DeepEqual(keyed(a), keyed(b)) }

// WithDefaults supplies only absent fields; it does not replace named entries.
func SharedWithDefaults(doc SharedDocument) SharedDocument {
	out := Shared(Defaults().File)
	for k, v := range doc {
		out[k] = v
	}
	return out
}
