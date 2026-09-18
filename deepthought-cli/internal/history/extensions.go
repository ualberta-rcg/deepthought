package history

import (
	"encoding/json"
	"fmt"
)

// ReadDocExt is the typed payload for a "read" probe.
type ReadDocExt struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

// BashExt is the typed payload for a "bash" probe.
type BashExt struct {
	Command   string `json:"command"`
	TimeoutMs int    `json:"timeout_ms"`
}

// ExtensionFactory creates a new empty value for a known probe type. The
// registry is populated at startup by tool packages (or cmd/deepthought-cli/main.go) so
// that history stays decoupled from specific tools.
type ExtensionFactory func() any

var extensionRegistry = map[string]ExtensionFactory{}

// RegisterExtensionType maps a probe type (wire tool name) to a factory that
// produces an empty extension value. Callers can then use Probe.ExtensionAs to
// decode into that type safely.
func RegisterExtensionType(probeType string, factory ExtensionFactory) {
	extensionRegistry[probeType] = factory
}

// ExtensionTypeFor returns the registered factory for a probe type, or nil.
func ExtensionTypeFor(probeType string) ExtensionFactory {
	return extensionRegistry[probeType]
}

// ExtensionAs unmarshals the probe's Extension payload into v. v should be a
// pointer to a registered extension type such as *ReadDocExt or *BashExt.
func (p *Probe) ExtensionAs(v any) error {
	if len(p.Extension) == 0 {
		return fmt.Errorf("probe %s has no extension payload", p.ID)
	}
	return json.Unmarshal(p.Extension, v)
}

// SetExtension marshals v into the probe's Extension payload. It is typically
// used by tests or by a backend loader that reconstructs objects from rows.
func (p *Probe) SetExtension(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("probe %s: marshal extension: %w", p.ID, err)
	}
	p.Extension = b
	return nil
}

// TypedExtension returns a newly allocated registered extension value for the
// probe's ProbeType, or nil if the type is unknown/unregistered.
func (p *Probe) TypedExtension() any {
	factory := extensionRegistry[p.ProbeType]
	if factory == nil {
		return nil
	}
	return factory()
}
