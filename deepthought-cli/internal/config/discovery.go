package config

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"deepthought-cli/internal/credential"
)

const AlephAnthropicURL = "https://inference.vulcan.alliancecan.ca/anthropic"
const LegacyAlephURL = "https://inference.kubeflow.vulcan.alliancecan.ca/serving/api/v1"
const DiscoveryFileLimit = 512 << 10

// Candidate contains a reviewable description, never printable credential data.
type Candidate struct {
	ID, Name, URL, Wire, Source string
	Untrusted                   bool
	HasCredential               bool
	value                       string
	envRef                      string
}

func (c Candidate) String() string {
	return c.Name + " · " + c.URL + " · " + c.Source + " · credential [masked]"
}
func (c Candidate) GoString() string { return c.String() }

type DiscoveryOptions struct {
	Home, WorkDir string
	LookupEnv     func(string) string
}

var assignment = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

// LiteralAssignments intentionally accepts no expansion or shell execution.
func LiteralAssignments(raw []byte) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		m := assignment.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[2])
		if strings.ContainsAny(v, "`$\x00\r") {
			continue
		}
		if len(v) > 1 && (v[0] == '\'' || v[0] == '"') {
			quote := v[0]
			end := strings.IndexByte(v[1:], quote)
			if end < 0 {
				continue
			}
			end++
			tail := strings.TrimSpace(v[end+1:])
			if tail != "" && !strings.HasPrefix(tail, "#") {
				continue
			}
			v = v[1:end]
		} else {
			if i := strings.Index(v, " #"); i >= 0 {
				v = v[:i]
			}
			if strings.ContainsAny(v, " \t;|&<>()\\\"'") {
				continue
			}
		}
		out[m[1]] = v
	}
	return out
}

func readDiscoveryFile(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular configuration file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > DiscoveryFileLimit {
		return nil, fmt.Errorf("unsupported configuration file")
	}
	if stat, ok := st.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Getuid()) {
		return nil, fmt.Errorf("configuration is owned by another user")
	}
	b, err := io.ReadAll(io.LimitReader(f, DiscoveryFileLimit+1))
	if len(b) > DiscoveryFileLimit {
		return nil, fmt.Errorf("configuration too large")
	}
	return b, err
}

func Discover(o DiscoveryOptions) []Candidate {
	if o.LookupEnv == nil {
		o.LookupEnv = os.Getenv
	}
	var out []Candidate
	add := func(values map[string]string, source string, project, process bool) {
		specs := []struct{ name, key, base, wire, def string }{
			{"Aleph", "TYK_KEY", "ALEPH_BASE_URL", "openai", DefaultBaseURL},
			{"OpenAI", "OPENAI_API_KEY", "OPENAI_BASE_URL", "openai", "https://api.openai.com/v1"},
			{"Anthropic", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "anthropic", "https://api.anthropic.com"},
			{"Anthropic", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "anthropic", "https://api.anthropic.com"},
			{"DeepSeek", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL", "openai", "https://api.deepseek.com/v1"},
		}
		for _, spec := range specs {
			value := values[spec.key]
			if value == "" {
				continue
			}
			base := strings.TrimRight(values[spec.base], "/")
			if base == "" {
				base = spec.def
			}
			if base == LegacyAlephURL {
				base = DefaultBaseURL
			}
			u, err := url.Parse(base)
			if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
				continue
			}
			sum := sha256.Sum256([]byte(source + "|" + spec.key + "|" + base))
			c := Candidate{ID: hex.EncodeToString(sum[:12]), Name: spec.name, URL: base, Wire: spec.wire, Source: source + " (" + spec.key + ")", HasCredential: true, Untrusted: project, value: value}
			if process {
				c.envRef = "$" + spec.key
			}
			credential.Register("candidate:"+c.ID, value)
			out = append(out, c)
		}
	}
	values := map[string]string{}
	for _, k := range []string{"TYK_KEY", "ALEPH_BASE_URL", "OPENAI_API_KEY", "OPENAI_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL"} {
		values[k] = o.LookupEnv(k)
	}
	add(values, "process environment", false, true)
	files := []struct {
		path    string
		project bool
	}{
		{filepath.Join(o.Home, ".aleph_tyk.env"), false}, {filepath.Join(o.Home, ".bashrc"), false},
		{filepath.Join(o.Home, ".bash_profile"), false}, {filepath.Join(o.Home, ".profile"), false},
	}
	if o.WorkDir != "" {
		files = append(files, struct {
			path    string
			project bool
		}{filepath.Join(o.WorkDir, ".env"), true})
		// Walk parent directory names only, stopping at the repository marker.
		for dir, depth := o.WorkDir, 0; depth < 32; depth++ {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				if dir != o.WorkDir {
					files = append(files, struct {
						path    string
						project bool
					}{filepath.Join(dir, ".env"), true})
				}
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	for _, file := range files {
		if raw, err := readDiscoveryFile(file.path); err == nil {
			add(LiteralAssignments(raw), file.path, file.project, false)
		}
	}
	for _, name := range []string{filepath.Join(o.Home, ".claude", "settings.json"), filepath.Join(o.Home, ".claude.json")} {
		if raw, err := readDiscoveryFile(name); err == nil {
			var v struct {
				Env map[string]string `json:"env"`
			}
			if json.Unmarshal(raw, &v) == nil {
				add(v.Env, name, false, false)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

// Adopt is called only after review; it never connects to a provider.
func (c Candidate) Adopt(configPath string) (Provider, error) {
	ref := c.envRef
	if ref == "" {
		id := "DISCOVERED_" + strings.ToUpper(c.ID)
		if err := writeSecrets(filepath.Join(filepath.Dir(configPath), "secrets.env"), map[string]string{id: c.value}); err != nil {
			return Provider{}, err
		}
		ref = "secret:" + id
		credential.Register(ref, c.value)
	}
	return Provider{Name: c.Name, BaseURL: c.URL, Wire: c.Wire, APIKey: ref}, nil
}
