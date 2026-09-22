// Package credential holds process-local secrets separately from settings.
// References are persisted; resolved values never enter settings snapshots.
package credential

import (
	"os"
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
	for _, value := range vault.values {
		if value != "" {
			text = strings.ReplaceAll(text, value, "[redacted]")
		}
	}
	return text
}
