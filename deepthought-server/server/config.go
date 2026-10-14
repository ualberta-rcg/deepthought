package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ServerConfig is the file-backed configuration for deepthought-server
// (loaded from --config, $DEEPTHOUGHT_SERVER_CONFIG, or <dataDir>/server.json).
// Environment variables always override file values so container deployments
// and local dev work seamlessly.
type ServerConfig struct {
	ListenAddr      string     `json:"listen_addr"`
	DataDir         string     `json:"data_dir,omitempty"`
	MySQLDSN        string     `json:"mysql_dsn,omitempty"`
	VaultKey        string     `json:"vault_key,omitempty"`
	AdminUsers      []string   `json:"admin_users,omitempty"`
	SessionTTLHours int        `json:"session_ttl_hours"`
	Auth            AuthConfig `json:"auth"`
}

// AuthConfig controls how users authenticate to deepthought-server.
// By default Mode is "password" and OIDC.Enabled is false so current shared-password
// authentication remains active unless an operator enables OIDC in server.json.
type AuthConfig struct {
	// Mode is "password" (default), "oidc", or "hybrid" (both password and OIDC).
	Mode         string     `json:"mode"`
	Password     string     `json:"password,omitempty"`
	PasswordFile string     `json:"password_file,omitempty"`
	OIDC         OIDCConfig `json:"oidc"`
}

// OIDCConfig configures the OpenID Connect Relying Party, pre-populated with
// Digital Research Alliance of Canada (DRAC) defaults matching Vulcan's
// Open OnDemand portal (/etc/ood/config/ood_portal.yml).
type OIDCConfig struct {
	Enabled              bool     `json:"enabled"`
	Issuer               string   `json:"issuer"`
	MetadataURL          string   `json:"metadata_url"`
	ClientID             string   `json:"client_id"`
	ClientSecret         string   `json:"client_secret,omitempty"`
	ClientSecretEnv      string   `json:"client_secret_env,omitempty"`
	RedirectURI          string   `json:"redirect_uri"`
	Scopes               []string `json:"scopes"`
	RemoteUserClaim      string   `json:"remote_user_claim"`
	EmailClaim           string   `json:"email_claim"`
	GroupsClaim          string   `json:"groups_claim,omitempty"`
	AllowedGroups        []string `json:"allowed_groups,omitempty"`
	TrustProxyHeaders    bool     `json:"trust_proxy_headers"`
	ProxyUserHeader      string   `json:"proxy_user_header"`
	ProxyEmailHeader     string   `json:"proxy_email_header"`
	PostLoginHook        string   `json:"post_login_hook,omitempty"`
	PostLoginHookExports []string `json:"post_login_hook_exports,omitempty"`
}

// DefaultServerConfig returns the default server configuration with password
// auth active and DRAC OIDC pre-populated but disabled (enabled: false).
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		ListenAddr:      "127.0.0.1:8080",
		SessionTTLHours: 24,
		Auth: AuthConfig{
			Mode: "password",
			OIDC: OIDCConfig{
				Enabled:              false,
				Issuer:               "https://idp.alliancecan.ca",
				MetadataURL:          "https://idp.alliancecan.ca/.well-known/openid-configuration",
				ClientID:             "deepthought.vulcan.alliancecan.ca",
				ClientSecretEnv:      "DEEPTHOUGHT_OIDC_CLIENT_SECRET",
				RedirectURI:          "https://deepthought.vulcan.alliancecan.ca/oidc",
				Scopes:               []string{"openid", "profile", "email"},
				RemoteUserClaim:      "preferred_username",
				EmailClaim:           "email",
				GroupsClaim:          "groups",
				TrustProxyHeaders:    false,
				ProxyUserHeader:      "OIDC_CLAIM_preferred_username",
				ProxyEmailHeader:     "OIDC_CLAIM_email",
				PostLoginHook:        "/opt/ood/scripts/ood_pun_oidc_email.sh",
				PostLoginHookExports: []string{"OIDC_CLAIM_email"},
			},
		},
	}
}

// LoadServerConfig loads <configPath> (or <dataDir>/server.json when configPath
// is empty and the file exists), merges environment variable overrides, and
// returns the effective configuration.
func LoadServerConfig(configPath, dataDir string) (ServerConfig, error) {
	cfg := DefaultServerConfig()
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	explicit := strings.TrimSpace(configPath) != ""
	if !explicit {
		if envPath := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_SERVER_CONFIG")); envPath != "" {
			configPath = envPath
			explicit = true
		} else if dataDir != "" {
			candidate := filepath.Join(dataDir, "server.json")
			if _, err := os.Stat(candidate); err == nil {
				configPath = candidate
			}
		}
	}
	if configPath != "" {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			if explicit {
				return cfg, fmt.Errorf("read server config %s: %w", configPath, err)
			}
		} else if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse server config %s: %w", configPath, err)
		}
	}
	cfg.applyEnvOverrides()
	cfg.normalize()
	return cfg, nil
}

// WriteExampleServerConfig writes <dataDir>/server.example.json so operators
// have a self-documented template on disk without activating OIDC.
func WriteExampleServerConfig(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	example := DefaultServerConfig()
	example.AdminUsers = []string{"rahimk"}
	raw, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	path := filepath.Join(dataDir, "server.example.json")
	return os.WriteFile(path, raw, 0o600)
}

func (c *ServerConfig) applyEnvOverrides() {
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_DATA_DIR")); v != "" {
		c.DataDir = v
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_MYSQL_DSN")); v != "" {
		c.MySQLDSN = v
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_VAULT_KEY")); v != "" {
		c.VaultKey = v
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_ADMIN_USERS")); v != "" {
		var admins []string
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				admins = append(admins, name)
			}
		}
		c.AdminUsers = admins
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_SERVER_PASSWORD")); v != "" {
		c.Auth.Password = v
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_AUTH_MODE")); v != "" {
		c.Auth.Mode = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_OIDC_ENABLED")); v != "" {
		c.Auth.OIDC.Enabled = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_OIDC_ISSUER")); v != "" {
		c.Auth.OIDC.Issuer = v
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_OIDC_METADATA_URL")); v != "" {
		c.Auth.OIDC.MetadataURL = v
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_OIDC_CLIENT_ID")); v != "" {
		c.Auth.OIDC.ClientID = v
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_OIDC_CLIENT_SECRET")); v != "" {
		c.Auth.OIDC.ClientSecret = v
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_OIDC_REDIRECT_URI")); v != "" {
		c.Auth.OIDC.RedirectURI = v
	}
	if v := strings.TrimSpace(os.Getenv("DEEPTHOUGHT_OIDC_POST_LOGIN_HOOK")); v != "" {
		c.Auth.OIDC.PostLoginHook = v
	}
}

func (c *ServerConfig) normalize() {
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:8080"
	}
	if c.SessionTTLHours <= 0 {
		c.SessionTTLHours = 24
	}
	c.Auth.Mode = strings.ToLower(strings.TrimSpace(c.Auth.Mode))
	if c.Auth.Mode == "" {
		c.Auth.Mode = "password"
	}
	if c.Auth.OIDC.Issuer == "" {
		c.Auth.OIDC.Issuer = "https://idp.alliancecan.ca"
	}
	if c.Auth.OIDC.MetadataURL == "" {
		c.Auth.OIDC.MetadataURL = strings.TrimRight(c.Auth.OIDC.Issuer, "/") + "/.well-known/openid-configuration"
	}
	if len(c.Auth.OIDC.Scopes) == 0 {
		c.Auth.OIDC.Scopes = []string{"openid", "profile", "email"}
	}
	if c.Auth.OIDC.RemoteUserClaim == "" {
		c.Auth.OIDC.RemoteUserClaim = "preferred_username"
	}
	if c.Auth.OIDC.EmailClaim == "" {
		c.Auth.OIDC.EmailClaim = "email"
	}
	if c.Auth.OIDC.ProxyUserHeader == "" {
		c.Auth.OIDC.ProxyUserHeader = "OIDC_CLAIM_preferred_username"
	}
	if c.Auth.OIDC.ProxyEmailHeader == "" {
		c.Auth.OIDC.ProxyEmailHeader = "OIDC_CLAIM_email"
	}
}

// ResolvedClientSecret returns the OIDC client secret from ClientSecret or
// ClientSecretEnv.
func (o OIDCConfig) ResolvedClientSecret() string {
	if o.ClientSecret != "" {
		return o.ClientSecret
	}
	if o.ClientSecretEnv != "" {
		return strings.TrimSpace(os.Getenv(o.ClientSecretEnv))
	}
	return strings.TrimSpace(os.Getenv("DEEPTHOUGHT_OIDC_CLIENT_SECRET"))
}

// PasswordEnabled reports whether shared-password login is active.
func (a AuthConfig) PasswordEnabled(effectivePassword string) bool {
	if effectivePassword == "" {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(a.Mode))
	return mode == "" || mode == "password" || mode == "hybrid"
}

// OIDCEnabled reports whether OIDC authentication is active.
func (a AuthConfig) OIDCEnabled() bool {
	if !a.OIDC.Enabled {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(a.Mode))
	return mode == "oidc" || mode == "hybrid" || a.OIDC.Enabled
}
