package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"annorax/internal/babel"
	"annorax/internal/config"
	"annorax/internal/history"
	"annorax/internal/tui"
	"annorax/internal/unimatrix"
)

// Settings is the live, shared configuration handle. The TUI reads snapshots
// from it, the settings editor writes through it (validate → save to disk →
// swap), and the chat loop resolves roles through it per request so a role
// change takes effect without a restart. Safe for concurrent use — one
// instance is shared by every SSH session.
type Settings struct {
	mu       sync.RWMutex
	cfg      *config.Config
	path     string
	pool     *unimatrix.Pool
	breakers map[string]*providerBreaker
}

type providerBreaker struct {
	failures  int
	openUntil time.Time
}

// NewSettings builds the handle from the loaded config and its file path.
func NewSettings(cfg *config.Config, path string) *Settings {
	return &Settings{cfg: cfg, path: path, pool: unimatrix.NewPool(), breakers: map[string]*providerBreaker{}}
}

// Path returns the resolved settings file path (for the Overview page).
func (s *Settings) Path() string { return s.path }

// Snapshot returns a copy of the current config for screens to render. Lists
// and maps are deep-copied so editors can mutate freely before saving.
func (s *Settings) Snapshot() config.File {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneFile(s.cfg.File)
}

func cloneFile(f config.File) config.File {
	out := f
	out.Providers = append([]config.Provider(nil), f.Providers...)
	for i := range out.Providers {
		out.Providers[i].Tags = append([]string(nil), f.Providers[i].Tags...)
	}
	out.Models = append([]unimatrix.Model(nil), f.Models...)
	for i := range out.Models {
		out.Models[i].Capabilities = append([]unimatrix.Capability(nil), f.Models[i].Capabilities...)
		out.Models[i].Tags = append([]string(nil), f.Models[i].Tags...)
	}
	out.Roles = make(map[string]string, len(f.Roles))
	for k, v := range f.Roles {
		out.Roles[k] = v
	}
	out.Routes = make(map[string]config.Route, len(f.Routes))
	for k, v := range f.Routes {
		v.Prefer = append([]string(nil), v.Prefer...)
		out.Routes[k] = v
	}
	if f.Permissions != nil {
		p := *f.Permissions
		p.Allow = append([]string(nil), f.Permissions.Allow...)
		p.Ask = append([]string(nil), f.Permissions.Ask...)
		p.Deny = append([]string(nil), f.Permissions.Deny...)
		out.Permissions = &p
	}
	if f.StatusLine != nil {
		s := *f.StatusLine
		s.Segments = append([]string(nil), f.StatusLine.Segments...)
		out.StatusLine = &s
	}
	return out
}

// Save validates f, writes it to disk, and swaps the live config. The client
// pool is dropped wholesale on any change — stale clients cost one lazy
// rebuild, and URL/key edits never linger.
func (s *Settings) Save(f config.File) error {
	cfg, err := config.Validate(f)
	if err != nil {
		return err
	}
	if err := config.Save(s.path, &f); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	s.pool = unimatrix.NewPool()
	return nil
}

// HasAgenticModel reports whether an agentic-capable model is configured AND its
// provider has a non-empty API key — i.e. the chat loop can actually run. It is
// the gate the splash uses to decide between "straight to a new chat" and "open
// Settings at the add-provider area".
func (s *Settings) HasAgenticModel() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.cfg.RoleModel(unimatrix.RoleAgentic)
	if err != nil {
		return false
	}
	p, err := s.cfg.ProviderFor(m)
	if err != nil {
		return false
	}
	return p.ExpandedKey() != ""
}

func (s *Settings) Effort() babel.Effort {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return babel.Effort(s.cfg.EffortLevel())
}

func (s *Settings) MaxTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.CompletionTokens()
}

func (s *Settings) Temperature() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.SamplingTemperature()
}

// CycleModel advances the agentic role (the one driving the chat loop) to the
// next agentic-capable model in the list, persists, and returns the new label.
// Used by the /model slash command to switch models mid-session.
func (s *Settings) CycleModel() (string, error) {
	// Build the candidate list under the read lock, then release before Save.
	s.mu.RLock()
	cur, _ := s.cfg.RoleModel(unimatrix.RoleAgentic)
	var cands []unimatrix.Model
	for _, m := range s.cfg.Models {
		if m.Agentic() {
			cands = append(cands, m)
		}
	}
	s.mu.RUnlock()
	if len(cands) == 0 {
		return "", fmt.Errorf("no agentic models configured")
	}
	at := 0
	for i, c := range cands {
		if c.ID == cur.ID {
			at = i
		}
	}
	next := cands[(at+1)%len(cands)]
	f := s.Snapshot()
	if f.Roles == nil {
		f.Roles = map[string]string{}
	}
	f.Roles[unimatrix.RoleAgentic] = next.ID
	f.Roles[unimatrix.RoleChat] = next.ID // keep chat in sync with what's driving
	if err := s.Save(f); err != nil {
		return "", err
	}
	if next.Label != "" {
		return next.Label, nil
	}
	return next.ID, nil
}

func (s *Settings) CycleEffort() (babel.Effort, error) {
	order := []babel.Effort{babel.EffortOff, babel.EffortLow, babel.EffortMedium, babel.EffortHigh, babel.EffortMax}
	f := s.Snapshot()
	current := babel.Effort(f.Effort)
	if current == "" {
		current = babel.EffortMedium
	}
	at := 0
	for i, effort := range order {
		if effort == current {
			at = i
			break
		}
	}
	next := order[(at+1)%len(order)]
	f.Effort = string(next)
	if err := s.Save(f); err != nil {
		return "", err
	}
	return next, nil
}

// SetEffort sets the reasoning effort, validates, persists, and hot-swaps. Used
// by the effort overlay.
func (s *Settings) SetEffort(e babel.Effort) error {
	f := s.Snapshot()
	f.Effort = string(e)
	return s.Save(f)
}

// AgenticModels returns the models that can drive the chat loop (chat+tools),
// in config order. Used by the model chooser.
func (s *Settings) AgenticModels() []unimatrix.Model {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []unimatrix.Model
	for _, m := range s.cfg.Models {
		if m.Agentic() {
			out = append(out, m)
		}
	}
	return out
}

// AgenticModelID returns the wire id of the model currently driving the chat
// (the agentic role), or "" if none.
func (s *Settings) AgenticModelID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.cfg.RoleModel(unimatrix.RoleAgentic)
	if err != nil {
		return ""
	}
	return m.ID
}

// ProviderStatus returns one cached status row per configured provider for the
// Status page. State is derived from the circuit breaker with NO network: "ok"
// (used, breaker closed), "degraded" (breaker open), or "idle" (never used).
func (s *Settings) ProviderStatus() []tui.ProviderRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]tui.ProviderRow, 0, len(s.cfg.Providers))
	for _, p := range s.cfg.Providers {
		state := "idle"
		if b := s.breakers[p.Name]; b != nil {
			if !b.openUntil.IsZero() && time.Now().Before(b.openUntil) {
				state = "degraded"
			} else {
				state = "ok"
			}
		}
		out = append(out, tui.ProviderRow{
			Name:   p.Name,
			Wire:   p.Wire,
			Tags:   strings.Join(p.Tags, ","),
			KeySet: p.ExpandedKey() != "",
			State:  state,
		})
	}
	return out
}

// SetAgenticModel sets the running model (agentic + chat roles), validates,
// persists, and hot-swaps. Used by the model chooser. Returns the label.
func (s *Settings) SetAgenticModel(id string) (string, error) {
	f := s.Snapshot()
	found := false
	for _, m := range f.Models {
		if m.ID == id {
			if !m.Agentic() {
				return "", fmt.Errorf("model %q is not agentic", id)
			}
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("unknown model %q", id)
	}
	if f.Roles == nil {
		f.Roles = map[string]string{}
	}
	f.Roles[unimatrix.RoleAgentic] = id
	f.Roles[unimatrix.RoleChat] = id
	if err := s.Save(f); err != nil {
		return "", err
	}
	for _, m := range f.Models {
		if m.ID == id {
			if m.Label != "" {
				return m.Label, nil
			}
			return m.ID, nil
		}
	}
	return id, nil
}

func (s *Settings) ImportProviders() (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	candidates, err := config.DiscoverClaudeProviders(home)
	if err != nil {
		return 0, err
	}
	f := s.Snapshot()
	added, err := config.MergeImports(&f, candidates, s.path)
	if err != nil {
		return 0, err
	}
	if err := s.Save(f); err != nil {
		return 0, err
	}
	return added, nil
}

// RoleClient resolves a role to its model and a pooled client for the model's
// provider. Unassigned roles fall back to the chat role (see RoleModel).
func (s *Settings) RoleClient(role string) (*babel.Client, unimatrix.Model, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, err := s.cfg.RoleModel(role)
	if err != nil {
		return nil, unimatrix.Model{}, err
	}
	c, err := s.clientForModel(m)
	return c, m, err
}

// ClientFor resolves a pooled client for a specific model ID (by wire id).
// Used by the settings editor's model-test.
func (s *Settings) ClientFor(modelID string) (*babel.Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.cfg.FindModel(modelID)
	if !ok {
		return nil, fmt.Errorf("settings: unknown model %q", modelID)
	}
	return s.clientForModel(m)
}

// ProviderClient resolves a pooled client for a named provider. Used by the
// settings editor's "list models" discovery.
func (s *Settings) ProviderClient(providerName string) (*babel.Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.cfg.FindProvider(providerName)
	if !ok {
		return nil, fmt.Errorf("settings: unknown provider %q", providerName)
	}
	return s.pool.Client(p.Name, p.BaseURL, p.ExpandedKey(), p.Wire, providerTimeout(p)), nil
}

// clientForModel returns the pooled client for a model's provider (openai only
// for now). Caller holds the read lock.
func (s *Settings) clientForModel(m unimatrix.Model) (*babel.Client, error) {
	p, err := s.cfg.ProviderFor(m)
	if err != nil {
		return nil, err
	}
	return s.pool.Client(p.Name, p.BaseURL, p.ExpandedKey(), p.Wire, providerTimeout(p)), nil
}

// RouteClient resolves a declarative route while enforcing sensitivity and
// hard provider budgets. Fallback never relaxes the label.
func (s *Settings) RouteClient(routeName string, sensitivity history.Sensitivity, estimatedTokens int, estimatedUSD float64) (*babel.Client, unimatrix.Model, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	route, ok := s.cfg.Routes[routeName]
	if !ok {
		route = config.Route{Capability: unimatrix.CapGenerate}
		if routeName == "embed" {
			route.Capability = unimatrix.CapEmbed
		}
	}
	preference := map[string]int{}
	for i, provider := range route.Prefer {
		preference[provider] = i
	}
	type candidate struct {
		model    unimatrix.Model
		provider config.Provider
		rank     int
	}
	var candidates []candidate
	for _, model := range s.cfg.Models {
		if !model.Can(route.Capability) || (route.NeedsTools && !model.Can(unimatrix.CapToolUse)) {
			continue
		}
		provider, err := s.cfg.ProviderFor(model)
		if err != nil || providerClearance(provider.Clearance) < sensitivity {
			continue
		}
		if provider.MaxTokens > 0 && estimatedTokens > provider.MaxTokens {
			continue
		}
		if provider.MaxUSD > 0 && estimatedUSD > provider.MaxUSD {
			continue
		}
		if breaker := s.breakers[provider.Name]; breaker != nil && time.Now().Before(breaker.openUntil) {
			continue
		}
		rank := len(preference) + 1
		if preferred, exists := preference[provider.Name]; exists {
			rank = preferred
		}
		candidates = append(candidates, candidate{model: model, provider: provider, rank: rank})
	}
	if len(candidates) == 0 {
		return nil, unimatrix.Model{}, fmt.Errorf("settings: no %s route eligible for %s sensitivity and budget", routeName, sensitivity)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].rank < candidates[j].rank })
	chosen := candidates[0]
	return s.pool.Client(chosen.provider.Name, chosen.provider.BaseURL, chosen.provider.ExpandedKey(), chosen.provider.Wire, providerTimeout(chosen.provider)), chosen.model, nil
}

func providerClearance(value string) history.Sensitivity {
	switch value {
	case "secret":
		return history.SensitivitySecret
	case "restricted":
		return history.SensitivityRestricted
	case "internal":
		return history.SensitivityInternal
	default:
		return history.SensitivityPublic
	}
}

// ReportProviderResult drives a provider circuit breaker.
func (s *Settings) ReportProviderResult(provider string, resultErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.breakers[provider]
	if state == nil {
		state = &providerBreaker{}
		s.breakers[provider] = state
	}
	if resultErr == nil {
		state.failures, state.openUntil = 0, time.Time{}
		return
	}
	state.failures++
	cfg, _ := s.cfg.FindProvider(provider)
	limit := cfg.MaxFailures
	if limit <= 0 {
		limit = 3
	}
	if state.failures >= limit {
		wait := time.Duration(cfg.CooldownMS) * time.Millisecond
		if wait <= 0 {
			wait = time.Minute
		}
		state.openUntil = time.Now().Add(wait)
	}
}

func providerTimeout(p config.Provider) time.Duration {
	if p.TimeoutMS > 0 {
		return time.Duration(p.TimeoutMS) * time.Millisecond
	}
	return 6 * time.Minute
}
