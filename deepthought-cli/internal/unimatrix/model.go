// Package unimatrix is DeepThought's inference core. It
// owns the model metadata shape (capabilities, roles, tags), the curated seed
// catalog, and the client Pool that hands out one Babel client per provider.
// The user's live model list lives in the config file; the seed catalog only
// fills in when nothing is on disk yet.
package unimatrix

import "strings"

// Capability is something a model can do. A model usually has several: the
// flagship Qwen is chat+tools+reasoning. "Agentic" is NOT a capability — it is
// derived (chat+tools) so the roles page can filter for models that can drive
// the tool loop.
type Capability string

const (
	CapChat       Capability = "chat"      // converses (every role needs at least this)
	CapTools      Capability = "tools"     // speaks the function-calling protocol
	CapReasoning  Capability = "reasoning" // emits a thinking trace when asked
	CapVision     Capability = "vision"    // accepts image inputs
	CapEmbedding  Capability = "embedding" // produces vector embeddings
	CapGenerate   Capability = "generate"
	CapEmbed      Capability = "embed"
	CapRerank     Capability = "rerank"
	CapClassify   Capability = "classify"
	CapTranscribe Capability = "transcribe"
	CapOCR        Capability = "ocr"
	CapDetect     Capability = "detect"
	CapToolUse    Capability = "tool_use"
	CapPredict    Capability = "predict"
)

// Capabilities lists every known capability, for checkbox editors.
func Capabilities() []Capability {
	return []Capability{
		CapGenerate, CapEmbed, CapRerank, CapClassify, CapTranscribe,
		CapOCR, CapVision, CapDetect, CapToolUse, CapPredict,
		CapChat, CapTools, CapReasoning, CapEmbedding,
	}
}

// Roles are the jobs models get assigned to on the Roles settings page. A role
// with no model assigned falls back to the chat role's model.
const (
	RoleChat      = "chat"      // drives the main conversation
	RoleAgentic   = "agentic"   // drives the tool loop (needs chat+tools)
	RolePlanning  = "planning"  // future planner (Cortex)
	RoleSummary   = "summary"   // history summarization
	RoleTombstone = "tombstone" // minimal tombstone markers (wants cheap+fast)
)

// Roles lists every assignable role, in display order.
func Roles() []string {
	return []string{RoleChat, RoleAgentic, RolePlanning, RoleSummary, RoleTombstone}
}

// Model is one entry in a model list. IDs are the wire identifiers sent to the
// provider; Provider names the config provider that serves it; everything else
// is display metadata or capability flags the agent loop keys off.
type Model struct {
	WireID         string       `json:"wire_id,omitempty"`
	ID             string       `json:"id"`
	Label          string       `json:"label"`
	Provider       string       `json:"provider"` // config provider name
	Params         string       `json:"params,omitempty"`
	Context        int          `json:"context,omitempty"`
	Capabilities   []Capability `json:"capabilities"`
	Tags           []string     `json:"tags,omitempty"`            // free-form: "coding", "fast", "local", …
	ReasoningStyle string       `json:"reasoning_style,omitempty"` // gptoss/qwen/gemma4/deepseek/anthropic/none
	Effort         string       `json:"effort,omitempty"`          // optional per-model override
}

func (m Model) RequestID() string {
	if m.WireID != "" {
		return m.WireID
	}
	return m.ID
}

// Can reports whether the model has capability c.
func (m Model) Can(c Capability) bool {
	for _, have := range m.Capabilities {
		if have == c || capabilityAlias(have, c) {
			return true
		}
	}
	return false
}

func capabilityAlias(have, want Capability) bool {
	return (have == CapChat && want == CapGenerate) ||
		(have == CapGenerate && want == CapChat) ||
		(have == CapTools && want == CapToolUse) ||
		(have == CapToolUse && want == CapTools) ||
		(have == CapEmbedding && want == CapEmbed) ||
		(have == CapEmbed && want == CapEmbedding)
}

// Agentic reports whether the model can drive the tool loop (chat + tools).
func (m Model) Agentic() bool { return m.Can(CapChat) && m.Can(CapTools) }

// EffectiveReasoningStyle returns the explicit style or infers a conservative
// style from the wire model ID.
func (m Model) EffectiveReasoningStyle() string {
	if m.ReasoningStyle != "" {
		return m.ReasoningStyle
	}
	id := strings.ToLower(m.ID)
	switch {
	case strings.Contains(id, "gpt-oss"):
		return "gptoss"
	case strings.Contains(id, "qwen"), strings.Contains(id, "qwq"):
		return "qwen"
	case strings.Contains(id, "gemma-4"):
		return "gemma4"
	case strings.Contains(id, "deepseek"), strings.Contains(id, "r1-"):
		return "deepseek"
	case strings.Contains(id, "claude"):
		return "anthropic"
	default:
		return "none"
	}
}

// Caps returns the capabilities as plain strings, for display.
func (m Model) Caps() []string {
	out := make([]string, len(m.Capabilities))
	for i, c := range m.Capabilities {
		out[i] = string(c)
	}
	return out
}

// builtin is the curated seed catalog — the models DeepThought knows out of the
// box on the Vulcan KServe gateway. It fills a fresh config file; once the
// user edits, the file is the truth and this list is inert. Curated from the
// Vulcan KServe registry (see memory: vulcan-inference-endpoint); every entry
// is tool-capable.
var builtin = []Model{
	{
		ID:       "qwen35-122b",
		Label:    "Qwen 3.5 122B",
		Provider: SeedProvider,
		Params:   "122B (10B active)",
		Context:  131072,
		Capabilities: []Capability{
			CapChat, CapTools, CapReasoning,
		},
	},
	{
		ID:       "gpt-oss-120b",
		Label:    "GPT-OSS 120B",
		Provider: SeedProvider,
		Params:   "117B MoE (5.1B active)",
		Context:  131072,
		Capabilities: []Capability{
			CapChat, CapTools, CapReasoning,
		},
	},
	{
		ID:       "gpt-oss-20b",
		Label:    "GPT-OSS 20B",
		Provider: SeedProvider,
		Params:   "21B MoE (3.6B active)",
		Context:  131072,
		Capabilities: []Capability{
			CapChat, CapTools, CapReasoning,
		},
		Tags: []string{"fast"},
	},
	{
		ID:       "gemma-4-26b-a4b",
		Label:    "Gemma 4 26B A4B",
		Provider: SeedProvider,
		Params:   "25.2B MoE (3.8B active)",
		Context:  131072,
		Capabilities: []Capability{
			CapChat, CapTools, CapReasoning, CapVision,
		},
	},
}

// SeedProvider is the provider name the seed catalog attaches to.
const SeedProvider = "vulcan"

// Catalog returns the seed model set. It returns a fresh slice copy so callers
// cannot mutate the package var.
func Catalog() []Model {
	out := make([]Model, len(builtin))
	copy(out, builtin)
	return out
}

// Lookup finds a seed model by wire ID, or reports !ok for user-added models
// (those live in the config file, not the seed catalog).
func Lookup(id string) (Model, bool) {
	for _, m := range builtin {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}
