package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"annorax/internal/unimatrix"
)

// ImportCandidate is a provider plus the models and optional secret discovered
// in another local CLI's configuration.
type ImportCandidate struct {
	Provider Provider
	Models   []unimatrix.Model
	Secret   string
	SecretID string
}

var safeEnvName = regexp.MustCompile(`[^A-Z0-9]+`)

// DiscoverClaudeProviders reads settings.json and settings.json.* without
// changing them. Tokens are returned separately and never copied into Provider.
func DiscoverClaudeProviders(home string) ([]ImportCandidate, error) {
	paths, err := filepath.Glob(filepath.Join(home, ".claude", "settings.json*"))
	if err != nil {
		return nil, err
	}
	paths = append(paths, filepath.Join(home, ".claude.json"))
	byURL := map[string]ImportCandidate{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		var settings struct {
			Env         map[string]string `json:"env"`
			EffortLevel string            `json:"effortLevel"`
		}
		if json.Unmarshal(raw, &settings) != nil {
			continue
		}
		base := strings.TrimRight(settings.Env["ANTHROPIC_BASE_URL"], "/")
		if base == "" {
			continue
		}
		name := providerName(base)
		secretID := "ANNORAX_" + safeEnvName.ReplaceAllString(strings.ToUpper(name), "_") + "_API_KEY"
		keyRef := "$" + secretID
		secret := settings.Env["ANTHROPIC_AUTH_TOKEN"]
		if strings.Contains(base, "vulcan.alliancecan.ca") {
			keyRef, secret, secretID = "$TYK_KEY", "", "TYK_KEY"
		} else if secret == "sk-none" {
			keyRef, secret, secretID = "sk-none", "", ""
		}
		timeout := atoi(settings.Env["API_TIMEOUT_MS"])
		candidate := ImportCandidate{
			Provider: Provider{Name: name, BaseURL: base, APIKey: keyRef, Wire: "anthropic", TimeoutMS: timeout},
			Secret:   secret, SecretID: secretID,
		}
		models := []string{
			settings.Env["ANTHROPIC_MODEL"],
			settings.Env["ANTHROPIC_DEFAULT_OPUS_MODEL"],
			settings.Env["ANTHROPIC_DEFAULT_SONNET_MODEL"],
			settings.Env["ANTHROPIC_DEFAULT_HAIKU_MODEL"],
		}
		seen := map[string]bool{}
		for _, id := range models {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			candidate.Models = append(candidate.Models, importedModel(id, name))
		}
		byURL[base] = candidate
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		byURL["https://api.openai.com/v1"] = ImportCandidate{
			Provider: Provider{Name: "OpenAI", BaseURL: "https://api.openai.com/v1", APIKey: "$OPENAI_API_KEY", Wire: "openai"},
		}
	}
	if os.Getenv("DEEPSEEK_API_KEY") != "" {
		byURL["https://api.deepseek.com/v1"] = ImportCandidate{
			Provider: Provider{Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", APIKey: "$DEEPSEEK_API_KEY", Wire: "openai"},
		}
	}
	out := make([]ImportCandidate, 0, len(byURL))
	for _, candidate := range byURL {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider.Name < out[j].Provider.Name })
	return out, nil
}

func importedModel(id, provider string) unimatrix.Model {
	model := unimatrix.Model{
		ID: id, Label: id, Provider: provider, Capabilities: []unimatrix.Capability{
			unimatrix.CapChat, unimatrix.CapTools, unimatrix.CapReasoning,
		},
	}
	model.ReasoningStyle = model.EffectiveReasoningStyle()
	return model
}

func providerName(base string) string {
	switch {
	case strings.Contains(base, "api.z.ai"):
		return "Z.ai"
	case strings.Contains(base, "api.kimi.com"):
		return "Kimi"
	case strings.Contains(base, "vulcan.alliancecan.ca"):
		return "Aleph Anthropic"
	case strings.Contains(base, "127.0.0.1"), strings.Contains(base, "localhost"):
		return "Local Proxy"
	default:
		return strings.TrimPrefix(strings.Split(strings.TrimPrefix(base, "https://"), "/")[0], "api.")
	}
}

// MergeImports adds non-duplicate providers and models, writes discovered
// secrets to secrets.env mode 0600, and exports them for the current process.
func MergeImports(file *File, candidates []ImportCandidate, configPath string) (int, error) {
	seenProviders := map[string]bool{}
	seenModels := map[string]bool{}
	for _, provider := range file.Providers {
		seenProviders[provider.Name] = true
	}
	for _, model := range file.Models {
		seenModels[model.ID] = true
	}
	secrets := map[string]string{}
	added := 0
	for _, candidate := range candidates {
		if !seenProviders[candidate.Provider.Name] {
			file.Providers = append(file.Providers, candidate.Provider)
			seenProviders[candidate.Provider.Name] = true
			added++
		}
		for _, model := range candidate.Models {
			if !seenModels[model.ID] {
				file.Models = append(file.Models, model)
				seenModels[model.ID] = true
			}
		}
		if candidate.SecretID != "" && candidate.Secret != "" {
			secrets[candidate.SecretID] = candidate.Secret
			_ = os.Setenv(candidate.SecretID, candidate.Secret)
		}
	}
	if len(secrets) > 0 {
		if err := writeSecrets(filepath.Join(filepath.Dir(configPath), "secrets.env"), secrets); err != nil {
			return 0, err
		}
	}
	return added, nil
}

func writeSecrets(path string, additions map[string]string) error {
	existing := map[string]string{}
	_ = loadSecretsInto(path, existing)
	for key, value := range additions {
		existing[key] = value
	}
	keys := make([]string, 0, len(existing))
	for key := range existing {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&b, "%s=%s\n", key, strconvQuote(existing[key]))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// LoadSecrets loads the sibling secrets.env before provider $VAR expansion.
func LoadSecrets(configPath string) error {
	return loadSecretsInto(filepath.Join(filepath.Dir(configPath), "secrets.env"), nil)
}

func loadSecretsInto(path string, capture map[string]string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, "\"")
		value = strings.ReplaceAll(value, `\"`, `"`)
		value = strings.ReplaceAll(value, `\\`, `\`)
		_ = os.Setenv(key, value)
		if capture != nil {
			capture[key] = value
		}
	}
	return sc.Err()
}

func strconvQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func atoi(value string) int {
	var n int
	_, _ = fmt.Sscanf(value, "%d", &n)
	return n
}
