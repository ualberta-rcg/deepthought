// Package credential holds process-local secrets separately from settings.
// References are persisted; resolved values never enter settings snapshots.
package credential

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
)

var vault = struct {
	sync.RWMutex
	values map[string]string
}{values: map[string]string{}}

func Register(ref, value string) {
	if ref == "" || value == "" {
		return
	}
	vault.Lock()
	defer vault.Unlock()
	vault.values[ref] = value
}

func Resolve(ref string) string {
	if strings.HasPrefix(ref, "$") {
		name := strings.Trim(strings.TrimPrefix(ref, "$"), "{}")
		if value, ok := os.LookupEnv(name); ok {
			Register(ref, value)
			return value
		}
	}
	vault.RLock()
	value, ok := vault.values[ref]
	vault.RUnlock()
	if ok {
		return value
	}
	if strings.HasPrefix(ref, "secret:") {
		return ""
	}
	if strings.HasPrefix(ref, "$") {
		value = os.ExpandEnv(ref)
	} else {
		value = ref
	}
	Register(ref, value)
	return value
}

func Redact(text string) string {
	vault.RLock()
	defer vault.RUnlock()
	values := make([]string, 0, len(vault.values))
	for _, value := range vault.values {
		if len(value) >= 4 {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "[redacted]")
	}
	return text
}

// RedactJSON preserves valid JSON even when a credential contains quotes.
func RedactJSON(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return Redact(raw)
	}
	var visit func(any) any
	visit = func(v any) any {
		switch x := v.(type) {
		case string:
			return Redact(x)
		case []any:
			for i := range x {
				x[i] = visit(x[i])
			}
		case map[string]any:
			for k, val := range x {
				x[k] = visit(val)
			}
		}
		return v
	}
	b, _ := json.Marshal(visit(value))
	return string(b)
}
