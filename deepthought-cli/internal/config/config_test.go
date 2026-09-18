package config

import (
	"os"
	"path/filepath"
	"testing"

	"deepthought-cli/internal/unimatrix"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadV2(t *testing.T) {
	t.Parallel()
	path := writeFile(t, `{
		"providers": [
			{"name": "vulcan", "base_url": "https://example/serving/api/v1", "api_key": "k", "wire": "openai", "tags": ["cad"]},
			{"name": "home", "base_url": "http://localhost:11434/v1", "wire": "openai", "tags": ["local"]}
		],
		"models": [
			{"id": "qwen35-122b", "label": "Qwen 3.5 122B", "provider": "vulcan", "capabilities": ["chat", "tools", "reasoning"]},
			{"id": "llama4", "label": "Llama 4", "provider": "home", "capabilities": ["chat"], "tags": ["coding"]}
		],
		"roles": {"chat": "llama4", "agentic": "qwen35-122b"}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 2 || len(cfg.Models) != 2 {
		t.Fatalf("got %d providers / %d models, want 2/2", len(cfg.Providers), len(cfg.Models))
	}
	m, err := cfg.RoleModel(unimatrix.RoleChat)
	if err != nil || m.ID != "llama4" {
		t.Errorf("chat role = %v, %v; want llama4", m.ID, err)
	}
	p, err := cfg.ProviderFor(m)
	if err != nil || p.Name != "home" {
		t.Errorf("provider for llama4 = %v, %v; want home", p.Name, err)
	}
}

// A v1 file (single provider + catalog IDs) migrates transparently: provider
// "default", seed models attached, default_model → chat role.
func TestLoadV1Migration(t *testing.T) {
	t.Parallel()
	path := writeFile(t, `{
		"provider": {"base_url": "https://example/serving/api/v1", "api_key": "k", "wire": "openai"},
		"default_model": "gpt-oss-120b",
		"summary_model": "gpt-oss-20b",
		"models": ["qwen35-122b", "gpt-oss-120b", "gpt-oss-20b"]
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "default" {
		t.Fatalf("providers = %+v, want one named default", cfg.Providers)
	}
	if cfg.Providers[0].BaseURL != "https://example/serving/api/v1" {
		t.Errorf("BaseURL = %q", cfg.Providers[0].BaseURL)
	}
	if len(cfg.Models) != 3 {
		t.Errorf("models = %d, want 3", len(cfg.Models))
	}
	for _, m := range cfg.Models {
		if m.Provider != "default" {
			t.Errorf("model %s attached to %q, want default", m.ID, m.Provider)
		}
	}
	if got := cfg.Roles[unimatrix.RoleChat]; got != "gpt-oss-120b" {
		t.Errorf("chat role = %q, want gpt-oss-120b", got)
	}
	if got := cfg.Roles[unimatrix.RoleSummary]; got != "gpt-oss-20b" {
		t.Errorf("summary role = %q, want gpt-oss-20b", got)
	}
}

// v1 migration drops catalog IDs the seed catalog doesn't know (they'd have
// been a load error before, but silently dropping beats stranding the user).
func TestV1MigrationDropsUnknownIDs(t *testing.T) {
	t.Parallel()
	path := writeFile(t, `{"provider": {"api_key": "k"}, "models": ["does-not-exist", "qwen35-122b"]}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Models) != 1 || cfg.Models[0].ID != "qwen35-122b" {
		t.Errorf("models = %+v, want just qwen35-122b", cfg.Models)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	p, ok := cfg.FindProvider(unimatrix.SeedProvider)
	if !ok || p.BaseURL != DefaultBaseURL {
		t.Errorf("seed provider = %+v, %v", p, ok)
	}
	m, err := cfg.RoleModel(unimatrix.RoleChat)
	if err != nil || m.ID != DefaultModelID {
		t.Errorf("chat role = %v, %v; want %s", m.ID, err, DefaultModelID)
	}
	if len(cfg.Models) == 0 {
		t.Error("default Models is empty; should be the seed catalog")
	}
}

func TestValidateDanglingReferences(t *testing.T) {
	t.Parallel()
	good := []Provider{{Name: "p", BaseURL: "http://x", APIKey: "k"}}
	goodModels := []unimatrix.Model{{ID: "m", Label: "M", Provider: "p", Capabilities: []unimatrix.Capability{"chat"}}}

	tests := []struct {
		name string
		f    File
	}{
		{"no providers", File{Models: goodModels}},
		{"no models", File{Providers: good}},
		{"dup provider", File{Providers: append(good, Provider{Name: "p", BaseURL: "http://y"}), Models: goodModels}},
		{"bad wire", File{Providers: []Provider{{Name: "p", BaseURL: "http://x", Wire: "smoke-signals"}}, Models: goodModels}},
		{"model → ghost provider", File{Providers: good, Models: []unimatrix.Model{{ID: "m", Provider: "ghost", Capabilities: []unimatrix.Capability{"chat"}}}}},
		{"role → ghost model", File{Providers: good, Models: goodModels, Roles: map[string]string{"chat": "ghost"}}},
		{"unknown role", File{Providers: good, Models: goodModels, Roles: map[string]string{"divination": "m"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Validate(tt.f); err == nil {
				t.Error("want validation error, got nil")
			}
		})
	}
}

// Unset roles fall back to the chat role; an unknown role model does too.
func TestRoleFallback(t *testing.T) {
	t.Parallel()
	cfg, err := Validate(File{
		Providers: []Provider{{Name: "p", BaseURL: "http://x", APIKey: "k"}},
		Models: []unimatrix.Model{
			{ID: "a", Provider: "p", Capabilities: []unimatrix.Capability{"chat"}},
			{ID: "b", Provider: "p", Capabilities: []unimatrix.Capability{"chat", "tools"}},
		},
		Roles: map[string]string{unimatrix.RoleChat: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{unimatrix.RoleAgentic, unimatrix.RoleSummary, unimatrix.RoleTombstone, unimatrix.RolePlanning} {
		m, err := cfg.RoleModel(role)
		if err != nil || m.ID != "b" {
			t.Errorf("role %s = %v, %v; want fallback b", role, m.ID, err)
		}
	}
}

func TestSaveRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	f := Defaults().File
	if err := Save(path, &f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 600 (the file carries API keys)", perm)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(cfg.Models) != len(f.Models) || cfg.Roles[unimatrix.RoleChat] != f.Roles[unimatrix.RoleChat] {
		t.Error("round trip changed the config")
	}
}

func TestExpandedKey(t *testing.T) {
	// Not parallel: t.Setenv mutates process env.
	t.Setenv("DEEPTHOUGHT_CLI_TEST_KEY", "s3cret")
	p := Provider{APIKey: "$DEEPTHOUGHT_CLI_TEST_KEY"}
	if got := p.ExpandedKey(); got != "s3cret" {
		t.Errorf("ExpandedKey = %q, want s3cret", got)
	}
	literal := Provider{APIKey: "plain"}
	if got := literal.ExpandedKey(); got != "plain" {
		t.Errorf("ExpandedKey literal = %q, want plain", got)
	}
}

func TestBaseDirEnv(t *testing.T) {
	// Not parallel: t.Setenv mutates process env.
	t.Setenv("DEEPTHOUGHT_CLI_HOME", "/custom/home")
	if got, err := BaseDir(); err != nil || got != "/custom/home" {
		t.Errorf("BaseDir = %q, %v; want /custom/home", got, err)
	}
	if got, err := DefaultPath(); err != nil || got != "/custom/home/config.json" {
		t.Errorf("DefaultPath = %q, %v; want /custom/home/config.json", got, err)
	}
	if got, err := DataDir(); err != nil || got != "/custom/home" {
		t.Errorf("DataDir = %q, %v; want /custom/home", got, err)
	}
}

func TestBaseDirDefault(t *testing.T) {
	// Not parallel: relies on the process env being free of the override.
	if os.Getenv("DEEPTHOUGHT_CLI_HOME") != "" {
		t.Skip("DEEPTHOUGHT_CLI_HOME set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".deepthought")
	if got, err := BaseDir(); err != nil || got != want {
		t.Errorf("BaseDir = %q, %v; want %q", got, err, want)
	}
}
