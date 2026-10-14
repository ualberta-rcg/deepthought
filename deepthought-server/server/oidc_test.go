package server

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultServerConfigKeepsPasswordActiveAndOIDCDisabled(t *testing.T) {
	dir := t.TempDir()
	if err := WriteExampleServerConfig(dir); err != nil {
		t.Fatalf("WriteExampleServerConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "server.example.json")); err != nil {
		t.Fatalf("server.example.json missing: %v", err)
	}
	cfg, err := LoadServerConfig("", dir)
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	if cfg.Auth.Mode != "password" || cfg.Auth.OIDC.Enabled {
		t.Fatalf("expected default mode=password and oidc.enabled=false, got %+v", cfg.Auth)
	}
	if cfg.Auth.OIDC.Issuer != "https://idp.alliancecan.ca" || cfg.Auth.OIDC.RemoteUserClaim != "preferred_username" || cfg.Auth.OIDC.EmailClaim != "email" {
		t.Fatalf("unexpected DRAC OIDC defaults: %+v", cfg.Auth.OIDC)
	}

	api := &API{
		Start:    time.Now(),
		DataDir:  dir,
		Password: "secret",
		Sessions: NewSessionStore(time.Hour),
		Config:   cfg,
		OIDC:     NewOIDCProvider(cfg.Auth.OIDC),
	}
	srv := httptest.NewServer(NewMux(api))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/auth/config")
	if err != nil {
		t.Fatalf("GET /api/v1/auth/config: %v", err)
	}
	defer resp.Body.Close()
	var authCfg map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&authCfg)
	if authCfg["password_enabled"] != true || authCfg["oidc_enabled"] != false {
		t.Fatalf("unexpected auth config: %+v", authCfg)
	}

	// OIDC login endpoint must return 503 when disabled.
	oidcResp, err := http.Get(srv.URL + "/api/v1/auth/oidc/login")
	if err != nil {
		t.Fatalf("GET /api/v1/auth/oidc/login: %v", err)
	}
	oidcResp.Body.Close()
	if oidcResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when OIDC disabled, got %d", oidcResp.StatusCode)
	}
}

func TestDRACOIDCFlowAndPostLoginHook(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	kid := "drac-key-1"

	var lastNonce string
	var idpSrv *httptest.Server
	idpMux := http.NewServeMux()
	idpMux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                 idpSrv.URL,
			"authorization_endpoint": idpSrv.URL + "/authorize",
			"token_endpoint":         idpSrv.URL + "/token",
			"userinfo_endpoint":      idpSrv.URL + "/userinfo",
			"jwks_uri":               idpSrv.URL + "/jwks",
		})
	})
	idpMux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		eBytes := big.NewInt(int64(priv.PublicKey.E)).Bytes()
		writeJSON(w, http.StatusOK, map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": kid,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(eBytes),
			}},
		})
	})
	idpMux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("code_verifier") == "" {
			http.Error(w, "missing pkce code_verifier", http.StatusBadRequest)
			return
		}
		headerJSON, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
		claimsJSON, _ := json.Marshal(map[string]any{
			"iss":                idpSrv.URL,
			"sub":                "drac-sub-rahimk",
			"aud":                "deepthought.vulcan.alliancecan.ca",
			"exp":                time.Now().Add(5 * time.Minute).Unix(),
			"nonce":              lastNonce,
			"preferred_username": "rahimk",
			"email":              "khoja1@ualberta.ca",
		})
		signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
		sum := sha256.Sum256([]byte(signingInput))
		sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		idToken := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": "acc_123",
			"id_token":     idToken,
			"token_type":   "Bearer",
		})
	})
	idpSrv = httptest.NewServer(idpMux)
	defer idpSrv.Close()

	// Create a test post-login hook script that writes user + email to a file.
	tmpDir := t.TempDir()
	hookOutput := filepath.Join(tmpDir, "hook_email.txt")
	hookScript := filepath.Join(tmpDir, "hook.sh")
	hookBody := "#!/bin/sh\nprintf '%s:%s' \"$2\" \"$OIDC_CLAIM_email\" > " + hookOutput + "\n"
	if err := os.WriteFile(hookScript, []byte(hookBody), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	cfg := DefaultServerConfig()
	cfg.Auth.Mode = "hybrid"
	cfg.Auth.OIDC.Enabled = true
	cfg.Auth.OIDC.Issuer = idpSrv.URL
	cfg.Auth.OIDC.MetadataURL = idpSrv.URL + "/.well-known/openid-configuration"
	cfg.Auth.OIDC.ClientID = "deepthought.vulcan.alliancecan.ca"
	cfg.Auth.OIDC.RedirectURI = "https://deepthought.vulcan.alliancecan.ca/oidc"
	cfg.Auth.OIDC.PostLoginHook = hookScript

	api := &API{
		Start:    time.Now(),
		DataDir:  tmpDir,
		Password: "secret",
		Sessions: NewSessionStore(time.Hour),
		Admins:   map[string]bool{"rahimk": true},
		Config:   cfg,
		OIDC:     NewOIDCProvider(cfg.Auth.OIDC),
	}
	srv := httptest.NewServer(NewMux(api))
	defer srv.Close()

	// 1. Start CLI device flow.
	devResp, err := http.Post(srv.URL+"/api/v1/auth/device/start", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("device/start: %v", err)
	}
	var devStart struct {
		DeviceCode      string `json:"device_code"`
		VerificationURI string `json:"verification_uri"`
	}
	_ = json.NewDecoder(devResp.Body).Decode(&devStart)
	devResp.Body.Close()
	if devStart.DeviceCode == "" {
		t.Fatalf("empty device_code")
	}

	// 2. Initiate OIDC login with device_code (no auto-follow redirect).
	noRedirect := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	loginResp, err := noRedirect.Get(srv.URL + devStart.VerificationURI)
	if err != nil {
		t.Fatalf("oidc/login: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", loginResp.StatusCode)
	}
	loc, err := url.Parse(loginResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("redirect url: %v", err)
	}
	state := loc.Query().Get("state")
	lastNonce = loc.Query().Get("nonce")
	if state == "" || lastNonce == "" || loc.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("missing PKCE/state/nonce on redirect: %s", loc.String())
	}

	// 3. Complete callback on /oidc (matching OOD's redirect URI path).
	cbReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/oidc?code=authcode123&state="+url.QueryEscape(state), nil)
	cbReq.Header.Set("Accept", "application/json")
	cbResp, err := noRedirect.Do(cbReq)
	if err != nil {
		t.Fatalf("oidc callback: %v", err)
	}
	var cbPayload struct {
		Token string `json:"token"`
		User  string `json:"user"`
		Email string `json:"email"`
	}
	_ = json.NewDecoder(cbResp.Body).Decode(&cbPayload)
	cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusOK || cbPayload.User != "rahimk" || cbPayload.Email != "khoja1@ualberta.ca" || cbPayload.Token == "" {
		t.Fatalf("unexpected callback response (%d): %+v", cbResp.StatusCode, cbPayload)
	}

	// 4. Verify post_login_hook wrote the OOD email file.
	hookBytes, err := os.ReadFile(hookOutput)
	if err != nil || string(hookBytes) != "rahimk:khoja1@ualberta.ca" {
		t.Fatalf("post_login_hook output = %q (err=%v)", string(hookBytes), err)
	}

	// 5. Poll device code and confirm CLI receives approved token + user + email.
	pollBody, _ := json.Marshal(map[string]string{"device_code": devStart.DeviceCode})
	pollResp, err := http.Post(srv.URL+"/api/v1/auth/device/poll", "application/json", bytes.NewReader(pollBody))
	if err != nil {
		t.Fatalf("device/poll: %v", err)
	}
	var pollPayload map[string]any
	_ = json.NewDecoder(pollResp.Body).Decode(&pollPayload)
	pollResp.Body.Close()
	if pollPayload["status"] != "approved" || pollPayload["user"] != "rahimk" || pollPayload["email"] != "khoja1@ualberta.ca" {
		t.Fatalf("unexpected device poll payload: %+v", pollPayload)
	}
}
