// Package config loads DeepThought's settings file: the provider list (unlimited
// inference backends, each with its own URL + key + wire family + tags), the
// model list (each model attached to a provider, with capabilities and
// free-form tags), and the role assignments (which model does which job).
//
// Path resolution: --config flag → BaseDir()/config.json, where BaseDir is the
// single per-user directory $DEEPTHOUGHT_CLI_HOME → $HOME/.deepthought —
// config, chats, and all other user state live there together. A missing file
// is not an error: built-in defaults (the KServe gateway + the seed catalog)
// let the TUI boot with nothing on disk. A v1 file (single provider + catalog
// model IDs) migrates transparently to the v2 shape in memory; it is rewritten
// on the next Save.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"deepthought-cli/internal/unimatrix"
)

// DefaultBaseURL is the Vulcan KServe inference gateway, with the /serving/api/v1
// prefix baked in so callers append "/chat/completions" directly. (The gateway's
// per-model metadata advertises a bare /v1/... path that 404s off the host root —
// the real prefix is /serving/api. See memory: vulcan-inference-endpoint.)
const DefaultBaseURL = "https://inference.kubeflow.vulcan.alliancecan.ca/serving/api/v1"

// DefaultModelID is used when nothing assigns the chat role.
const DefaultModelID = "qwen35-122b"

// Provider is one inference-backend connection: where to reach it, how to
// authenticate, which wire family it speaks, and free-form tags (suggested:
// local, external, usa, cad, china) for grouping on the settings page.
type Provider struct {
	Name          string   `json:"name"`
	BaseURL       string   `json:"base_url"` // gateway URL incl. version prefix
	APIKey        string   `json:"api_key"`  // literal token or "$ENV_VAR" reference
	Wire          string   `json:"wire"`     // "openai" | "anthropic"; empty defaults to "openai"
	TimeoutMS     int      `json:"timeout_ms,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	Jurisdiction  string   `json:"jurisdiction,omitempty"`
	Clearance     string   `json:"clearance,omitempty"`
	CatalogSource string   `json:"catalog_source,omitempty"`
	MaxFailures   int      `json:"max_failures,omitempty"`
	CooldownMS    int      `json:"cooldown_ms,omitempty"`
	MaxUSD        float64  `json:"max_usd,omitempty"`
	MaxTokens     int      `json:"max_tokens_budget,omitempty"`
}

type Route struct {
	Capability   unimatrix.Capability `json:"capability"`
	NeedsTools   bool                 `json:"needs_tools,omitempty"`
	MaxCost      float64              `json:"max_cost,omitempty"`
	MaxLatencyMS int                  `json:"max_latency_ms,omitempty"`
	Prefer       []string             `json:"prefer,omitempty"`
}

// ExpandedKey returns the API key with $VAR / ${VAR} environment references
// resolved. Expansion happens at use time, never before Save — the file must
// keep the "$VAR" form rather than baking in the resolved secret.
func (p Provider) ExpandedKey() string { return os.ExpandEnv(p.APIKey) }

// File is the on-disk JSON shape, v2 (the lowercase struct tags map to
// config.json).
type File struct {
	Providers      []Provider        `json:"providers"`
	Models         []unimatrix.Model `json:"models"`
	Roles          map[string]string `json:"roles"`              // role → model ID; unset roles fall back to chat
	Thinking       *bool             `json:"thinking,omitempty"` // nil = on
	Effort         string            `json:"effort,omitempty"`   // off/low/medium/high/max
	MaxTokens      int               `json:"max_tokens,omitempty"`
	Temperature    float64           `json:"temperature,omitempty"`
	PermissionMode string            `json:"permission_mode,omitempty"`
	Permissions    *Permissions      `json:"permissions,omitempty"` // v2 op-modes + rule lists
	Routes         map[string]Route  `json:"routes,omitempty"`

	// StatusLine configures the bottom chrome band.
	StatusLine *StatusLine `json:"status_line,omitempty"`

	// Appearance holds visual preferences.
	Appearance *Appearance `json:"appearance,omitempty"`

	// General / profile (optional, omitempty — no migration; absent fields are
	// empty). Surfaced in Settings › General.
	Language     string `json:"language,omitempty"`      // e.g. en, fr
	PrivacyLevel string `json:"privacy_level,omitempty"` // standard | strict | local-only
	Name         string `json:"name,omitempty"`
	Email        string `json:"email,omitempty"`
	Domain       string `json:"domain,omitempty"` // research domain, e.g. "chemistry"
	Org          string `json:"org,omitempty"`    // institution/group, e.g. "AMII"
	Notes        string `json:"notes,omitempty"`  // free-text context for the agent
}

// Permissions is the v2 permission block: an operation-mode name plus hybrid
// allow/ask/deny rule lists (reference-style "Bash(git *)", "Read(~/**)").
type Permissions struct {
	Mode  string   `json:"mode,omitempty"` // safe | safe-auto | auto | custom name
	Allow []string `json:"allow,omitempty"`
	Ask   []string `json:"ask,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// StatusLine configures the bottom chrome band.
type StatusLine struct {
	Enabled  bool     `json:"enabled,omitempty"`
	Segments []string `json:"segments,omitempty"` // cwd, git, model, mode, tokens, clock
	Command  string   `json:"command,omitempty"`  // optional shell whose stdout fills the bar
}

// Appearance is the visual-customization block. Pointers so "absent" is
// distinguishable from a set value and each field keeps its own default.
type Appearance struct {
	// TopBarLegend toggles the F-key legend row under the top bar. nil = on
	// (the default — the legend is the least-surprising state for a new user).
	TopBarLegend *bool `json:"top_bar_legend,omitempty"`

	// Sidebar controls the chat screen's live info column: "" or "auto" =
	// show on very wide terminals (>=160 cols); "on" = force (>=120 cols);
	// "off" = hide.
	Sidebar string `json:"sidebar,omitempty"`
}

// TopBarLegendOn reports the effective legend setting (default on when unset).
func (f File) TopBarLegendOn() bool {
	if f.Appearance != nil && f.Appearance.TopBarLegend != nil {
		return *f.Appearance.TopBarLegend
	}
	return true
}

// Config is the validated, in-memory settings. The File is embedded so callers
// read the lists directly; the Find*/RoleModel helpers do the lookups.
type Config struct {
	File
}

// FindProvider returns the named provider.
func (c *Config) FindProvider(name string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// FindModel returns the model with the given wire ID.
func (c *Config) FindModel(id string) (unimatrix.Model, bool) {
	for _, m := range c.Models {
		if m.ID == id {
			return m, true
		}
	}
	return unimatrix.Model{}, false
}

// ProviderFor returns the provider serving model m, or an error naming both.
func (c *Config) ProviderFor(m unimatrix.Model) (Provider, error) {
	p, ok := c.FindProvider(m.Provider)
	if !ok {
		return Provider{}, fmt.Errorf("config: model %q names unknown provider %q", m.ID, m.Provider)
	}
	return p, nil
}

// RoleModel resolves a role to its model. A role with no assignment (or an
// assignment to a since-deleted model) falls back to the chat role, then to
// the first model in the list.
func (c *Config) RoleModel(role string) (unimatrix.Model, error) {
	if id := c.Roles[role]; id != "" {
		if m, ok := c.FindModel(id); ok {
			return m, nil
		}
	}
	if id := c.Roles[unimatrix.RoleChat]; id != "" {
		if m, ok := c.FindModel(id); ok {
			return m, nil
		}
	}
	if len(c.Models) > 0 {
		return c.Models[0], nil
	}
	return unimatrix.Model{}, fmt.Errorf("config: no models configured")
}

// EffortLevel resolves the provider-neutral reasoning effort. Legacy thinking
// files migrate in memory: false means off, otherwise medium.
func (c *Config) EffortLevel() string {
	if c.Effort != "" {
		return c.Effort
	}
	if c.Thinking != nil && !*c.Thinking {
		return "off"
	}
	return "medium"
}

func (c *Config) CompletionTokens() int {
	if c.MaxTokens > 0 {
		return c.MaxTokens
	}
	return 8192
}

func (c *Config) SamplingTemperature() float64 {
	if c.Temperature > 0 {
		return c.Temperature
	}
	return 0.7
}

// BaseDir reports the single per-user DeepThought directory: config, chat
// transcripts, and all other user state live under it — one directory per
// home, so there is exactly one place to back up or move. Precedence:
// $DEEPTHOUGHT_CLI_HOME → $HOME/.deepthought.
func BaseDir() (string, error) {
	if p := os.Getenv("DEEPTHOUGHT_CLI_HOME"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve base dir: %w", err)
	}
	return filepath.Join(home, ".deepthought"), nil
}

// DefaultPath reports where the settings file lives when no --config flag is
// given: BaseDir()/config.json.
func DefaultPath() (string, error) {
	d, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// DataDir reports where DeepThought stores runtime data (chat transcripts,
// history.db). It is BaseDir — one directory per home for all user state.
func DataDir() (string, error) {
	return BaseDir()
}

// ChatDir reports the chats subdirectory under DataDir.
func ChatDir() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "chats"), nil
}

// Load reads and validates the settings file at path. A missing file yields built-in
// defaults (and no error) so the TUI boots out of the box. A present-but-invalid
// file (bad JSON, dangling provider/role references) is a hard error.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Defaults(), nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	f, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return Validate(f)
}

// parse decodes the settings JSON, transparently migrating the v1 shape
// (single "provider" block + catalog model IDs) to v2.
func parse(raw []byte) (File, error) {
	var probe struct {
		Provider json.RawMessage `json:"provider"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return File{}, err
	}
	if len(probe.Provider) > 0 {
		var old v1File
		if err := json.Unmarshal(raw, &old); err != nil {
			return File{}, err
		}
		return migrateV1(old), nil
	}

	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return File{}, err
	}
	return f, nil
}

// v1File is the pre-providers settings shape: one gateway block, the default
// and summary model as catalog IDs, and the enabled catalog ID list.
type v1File struct {
	Provider     Provider `json:"provider"`
	DefaultModel string   `json:"default_model"`
	SummaryModel string   `json:"summary_model"`
	Models       []string `json:"models"`
	Thinking     *bool    `json:"thinking"`
}

// migrateV1 converts a v1 file: its gateway becomes provider "default", its
// catalog IDs become seed-catalog models attached to it, default_model becomes
// the chat role, and summary_model the summary role. Empty fields backfill
// exactly as v1 did.
func migrateV1(old v1File) File {
	p := old.Provider
	p.Name = "default"
	if p.BaseURL == "" {
		p.BaseURL = DefaultBaseURL
	}

	ids := old.Models
	if len(ids) == 0 {
		cat := unimatrix.Catalog()
		ids = make([]string, len(cat))
		for i, m := range cat {
			ids[i] = m.ID
		}
	}
	models := make([]unimatrix.Model, 0, len(ids))
	for _, id := range ids {
		m, ok := unimatrix.Lookup(id)
		if !ok {
			continue // unknown seed IDs drop out of the migrated list
		}
		m.Provider = "default"
		models = append(models, m)
	}

	def := old.DefaultModel
	if def == "" {
		def = DefaultModelID
	}
	roles := map[string]string{unimatrix.RoleChat: def}
	if old.SummaryModel != "" {
		roles[unimatrix.RoleSummary] = old.SummaryModel
	}

	return File{
		Providers: []Provider{p},
		Models:    models,
		Roles:     roles,
		Thinking:  old.Thinking,
	}
}

// Defaults builds a Config from the built-in gateway + the seed catalog, for
// use when no settings file is present.
func Defaults() *Config {
	return &Config{File: File{
		Providers: []Provider{{
			Name:    unimatrix.SeedProvider,
			BaseURL: DefaultBaseURL,
			Wire:    "openai",
			Tags:    []string{"cad", "external"},
		}},
		Models: unimatrix.Catalog(),
		Roles:  map[string]string{unimatrix.RoleChat: DefaultModelID},
	}}
}

// Validate checks a File's internal references and returns it as a Config.
// Used by Load and by the settings editor before anything is written to disk.
func Validate(f File) (*Config, error) {
	if len(f.Providers) == 0 {
		return nil, fmt.Errorf("config: no providers")
	}
	seenP := make(map[string]bool, len(f.Providers))
	for i := range f.Providers {
		p := &f.Providers[i]
		if p.Name == "" {
			return nil, fmt.Errorf("config: provider with empty name")
		}
		if seenP[p.Name] {
			return nil, fmt.Errorf("config: duplicate provider %q", p.Name)
		}
		seenP[p.Name] = true
		if p.BaseURL == "" {
			return nil, fmt.Errorf("config: provider %q has no base_url", p.Name)
		}
		switch p.Wire {
		case "", "openai":
			p.Wire = "openai"
		case "anthropic", "json":
		default:
			return nil, fmt.Errorf("config: provider %q: unknown wire family %q", p.Name, p.Wire)
		}
		switch p.Clearance {
		case "", "public", "internal", "restricted", "secret":
		default:
			return nil, fmt.Errorf("config: provider %q: unknown clearance %q", p.Name, p.Clearance)
		}
	}
	if len(f.Models) == 0 {
		return nil, fmt.Errorf("config: no models")
	}
	seenM := make(map[string]bool, len(f.Models))
	for _, m := range f.Models {
		if m.ID == "" {
			return nil, fmt.Errorf("config: model with empty id")
		}
		if seenM[m.ID] {
			return nil, fmt.Errorf("config: duplicate model %q", m.ID)
		}
		seenM[m.ID] = true
		if !seenP[m.Provider] {
			return nil, fmt.Errorf("config: model %q names unknown provider %q", m.ID, m.Provider)
		}
	}
	switch f.Effort {
	case "", "off", "low", "medium", "high", "max":
	default:
		return nil, fmt.Errorf("config: unknown effort %q", f.Effort)
	}
	switch f.PermissionMode {
	case "", "review", "sandbox", "always-proceed", "safe", "safe-auto", "auto":
	default:
		return nil, fmt.Errorf("config: unknown permission mode %q", f.PermissionMode)
	}
	if f.Permissions != nil {
		switch f.Permissions.Mode {
		case "", "review", "sandbox", "always-proceed", "safe", "safe-auto", "auto":
		default:
			// custom mode names are allowed; no further validation here
		}
	}
	switch f.Language {
	case "", "en", "fr", "es", "de", "it", "pt", "zh", "ja", "ko", "ar", "hi":
	default:
		return nil, fmt.Errorf("config: unknown language %q", f.Language)
	}
	switch f.PrivacyLevel {
	case "", "standard", "strict", "local-only":
	default:
		return nil, fmt.Errorf("config: unknown privacy level %q", f.PrivacyLevel)
	}
	for role, id := range f.Roles {
		if !validRole(role) {
			return nil, fmt.Errorf("config: unknown role %q", role)
		}
		if !seenM[id] {
			return nil, fmt.Errorf("config: role %q names unknown model %q", role, id)
		}
	}
	return &Config{File: f}, nil
}

func validRole(role string) bool {
	for _, r := range unimatrix.Roles() {
		if r == role {
			return true
		}
	}
	return false
}

// Save writes f to path atomically (temp file + rename) with mode 0600 — the
// file carries API keys. The parent directory is created if needed.
func Save(path string, f *File) error {
	if _, err := Validate(*f); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}
