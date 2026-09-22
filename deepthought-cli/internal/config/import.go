package config

import (
	"bufio"
	"deepthought-cli/internal/credential"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"deepthought-cli/internal/unimatrix"
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
		secretID := "DEEPTHOUGHT_CLI_" + safeEnvName.ReplaceAllString(strings.ToUpper(name), "_") + "_API_KEY"
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
			unimatrix.CapChat,
		},
	}
	model.ReasoningStyle = "none"
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
			credential.Register("$"+candidate.SecretID, candidate.Secret)
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
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	existing := map[string]string{}
	// A read failure here must abort the merge: writing with an empty
	// "existing" map would clobber previously imported secrets. (A missing
	// file is not an error — loadSecretsInto treats NotExist as "none yet".)
	if err := loadSecretsInto(path, existing); err != nil {
		return fmt.Errorf("read existing secrets before merging: %w", err)
	}
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
	f, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(b.String()); err != nil {
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

// LoadSecrets loads the sibling secrets.env before provider $VAR expansion.
func LoadSecrets(configPath string) error {
	return loadSecretsInto(filepath.Join(filepath.Dir(configPath), "secrets.env"), nil)
}

func loadSecretsInto(path string, capture map[string]string) error {
	raw, err := readDiscoveryFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if decoded, err := strconv.Unquote(value); err == nil {
			value = decoded
		}
		credential.Register("secret:"+key, value)
		if os.Getenv(key) == "" {
			credential.Register("$"+key, value)
		}
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
