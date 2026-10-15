package server

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// oidcDiscovery holds the subset of /.well-known/openid-configuration we need.
type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

type jwksDoc struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type oidcPendingState struct {
	State        string
	Nonce        string
	CodeVerifier string
	DeviceCode   string
	Expires      time.Time
}

type oidcDeviceTicket struct {
	DeviceCode string
	UserCode   string
	Approved   bool
	Token      string
	User       string
	Email      string
	Expires    time.Time
}

// OIDCProvider manages discovery, JWKS verification, pending PKCE states,
// and device-code login tickets.
type OIDCProvider struct {
	cfg        OIDCConfig
	httpClient *http.Client

	mu          sync.Mutex
	disc        *oidcDiscovery
	discExpires time.Time
	keys        map[string]any // kid -> *rsa.PublicKey or *ecdsa.PublicKey
	keysExpires time.Time

	states  map[string]oidcPendingState
	devices map[string]*oidcDeviceTicket
}

// NewOIDCProvider constructs an OIDC provider from cfg.
func NewOIDCProvider(cfg OIDCConfig) *OIDCProvider {
	return &OIDCProvider{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		keys:       map[string]any{},
		states:     map[string]oidcPendingState{},
		devices:    map[string]*oidcDeviceTicket{},
	}
}

func randomBase64URL(bytesLen int) (string, error) {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (p *OIDCProvider) purgeLocked(now time.Time) {
	for k, st := range p.states {
		if now.After(st.Expires) {
			delete(p.states, k)
		}
	}
	for k, d := range p.devices {
		if now.After(d.Expires) {
			delete(p.devices, k)
		}
	}
}

func (p *OIDCProvider) discover(ctx context.Context) (*oidcDiscovery, error) {
	p.mu.Lock()
	if p.disc != nil && time.Now().Before(p.discExpires) {
		d := p.disc
		p.mu.Unlock()
		return d, nil
	}
	p.mu.Unlock()

	metaURL := p.cfg.MetadataURL
	if metaURL == "" {
		metaURL = strings.TrimRight(p.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery HTTP %d", resp.StatusCode)
	}
	var doc oidcDiscovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("oidc discovery decode: %w", err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return nil, errors.New("oidc discovery missing required endpoints")
	}

	p.mu.Lock()
	p.disc = &doc
	p.discExpires = time.Now().Add(15 * time.Minute)
	p.mu.Unlock()
	return &doc, nil
}

func (p *OIDCProvider) fetchKeys(ctx context.Context, jwksURI string, force bool) (map[string]any, error) {
	p.mu.Lock()
	if !force && len(p.keys) > 0 && time.Now().Before(p.keysExpires) {
		out := make(map[string]any, len(p.keys))
		for k, v := range p.keys {
			out[k] = v
		}
		p.mu.Unlock()
		return out, nil
	}
	p.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc jwks HTTP %d", resp.StatusCode)
	}
	var doc jwksDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("oidc jwks decode: %w", err)
	}
	parsed := map[string]any{}
	for idx, k := range doc.Keys {
		pub, err := parseJWK(k)
		if err != nil {
			continue
		}
		kid := k.Kid
		if kid == "" {
			kid = fmt.Sprintf("key_%d", idx)
		}
		parsed[kid] = pub
	}
	if len(parsed) == 0 {
		return nil, errors.New("oidc jwks: no usable public keys")
	}
	p.mu.Lock()
	p.keys = parsed
	p.keysExpires = time.Now().Add(15 * time.Minute)
	p.mu.Unlock()
	return parsed, nil
}

func parseJWK(k jwkKey) (any, error) {
	switch strings.ToUpper(k.Kty) {
	case "RSA":
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, err
		}
		e := 0
		for _, b := range eb {
			e = (e << 8) | int(b)
		}
		if e <= 1 {
			return nil, errors.New("invalid RSA exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
	case "EC":
		xb, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		yb, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported EC curve %q", k.Crv)
		}
		return &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(xb),
			Y:     new(big.Int).SetBytes(yb),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported kty %q", k.Kty)
	}
}

func (p *OIDCProvider) verifyIDToken(ctx context.Context, rawToken, expectedNonce string) (map[string]any, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed JWT")
	}
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("jwt header: %w", err)
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return nil, fmt.Errorf("jwt header json: %w", err)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("jwt payload: %w", err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("jwt signature: %w", err)
	}

	disc, err := p.discover(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := p.fetchKeys(ctx, disc.JWKSURI, false)
	if err != nil {
		return nil, err
	}
	key, ok := keys[hdr.Kid]
	if !ok && hdr.Kid != "" {
		if refreshed, err := p.fetchKeys(ctx, disc.JWKSURI, true); err == nil {
			key, ok = refreshed[hdr.Kid]
		}
	}
	if !ok && len(keys) == 1 {
		for _, only := range keys {
			key = only
			ok = true
		}
	}
	if !ok {
		return nil, fmt.Errorf("jwt key id %q not found in JWKS", hdr.Kid)
	}

	signingInput := []byte(parts[0] + "." + parts[1])
	if err := verifyJWTSignature(hdr.Alg, key, signingInput, sigBytes); err != nil {
		return nil, err
	}

	var claims map[string]any
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("jwt claims: %w", err)
	}

	// Validate issuer.
	iss, _ := claims["iss"].(string)
	wantIss := strings.TrimRight(disc.Issuer, "/")
	if wantIss == "" {
		wantIss = strings.TrimRight(p.cfg.Issuer, "/")
	}
	if strings.TrimRight(iss, "/") != wantIss {
		return nil, fmt.Errorf("jwt issuer mismatch: got %q, want %q", iss, wantIss)
	}

	// Validate audience.
	if p.cfg.ClientID != "" && !audienceMatches(claims["aud"], p.cfg.ClientID) {
		return nil, fmt.Errorf("jwt audience does not include %q", p.cfg.ClientID)
	}

	// Validate expiry with 60s skew allowance.
	expFloat, ok := claims["exp"].(float64)
	if !ok {
		return nil, errors.New("jwt missing exp claim")
	}
	if time.Now().Add(-60 * time.Second).After(time.Unix(int64(expFloat), 0)) {
		return nil, errors.New("jwt expired")
	}

	// Validate nonce when expected.
	if expectedNonce != "" {
		gotNonce, _ := claims["nonce"].(string)
		if gotNonce != expectedNonce {
			return nil, errors.New("jwt nonce mismatch")
		}
	}
	return claims, nil
}

func audienceMatches(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func verifyJWTSignature(alg string, key any, signingInput, sig []byte) error {
	var h crypto.Hash
	var hasher func() hash.Hash
	switch alg {
	case "RS256", "ES256":
		h, hasher = crypto.SHA256, sha256.New
	case "RS384", "ES384":
		h, hasher = crypto.SHA384, sha512.New384
	case "RS512", "ES512":
		h, hasher = crypto.SHA512, sha512.New
	default:
		return fmt.Errorf("unsupported JWT alg %q", alg)
	}
	d := hasher()
	d.Write(signingInput)
	digest := d.Sum(nil)

	switch strings.HasPrefix(alg, "RS") {
	case true:
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return errors.New("jwt alg is RSA but JWK is not RSA")
		}
		return rsa.VerifyPKCS1v15(pub, h, digest, sig)
	default:
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("jwt alg is ECDSA but JWK is not EC")
		}
		if len(sig)%2 != 0 {
			return errors.New("invalid ECDSA signature length")
		}
		r := new(big.Int).SetBytes(sig[:len(sig)/2])
		s := new(big.Int).SetBytes(sig[len(sig)/2:])
		if !ecdsa.Verify(pub, digest, r, s) {
			return errors.New("invalid ECDSA signature")
		}
		return nil
	}
}

func (a *API) oidcActive() bool {
	return a.Config.Auth.OIDCEnabled() && a.OIDC != nil
}

func (a *API) passwordActive() bool {
	return a.Config.Auth.PasswordEnabled(a.Password)
}

func (a *API) anyAuthConfigured() bool {
	return a.passwordActive() || a.oidcActive()
}

// authConfigHandler serves GET /api/v1/auth/config (unauthenticated) so the
// web UI and CLI can discover whether password auth, DRAC OIDC, or both are enabled.
func (a *API) authConfigHandler(w http.ResponseWriter, r *http.Request) {
	mode := a.Config.Auth.Mode
	if mode == "" {
		mode = "password"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":             mode,
		"password_enabled": a.passwordActive(),
		"oidc_enabled":     a.oidcActive(),
		"oidc_issuer":      a.Config.Auth.OIDC.Issuer,
		"oidc_login_url":   "/api/v1/auth/oidc/login",
	})
}

// authMeHandler serves GET /api/v1/auth/me (authenticated).
func (a *API) authMeHandler(w http.ResponseWriter, r *http.Request) {
	user := SessionUser(r)
	var email, oidcSub string
	if a.Users != nil {
		if rec, err := a.Users.GetUser(sanitizeUserID(user)); err == nil {
			email = rec.Email
			oidcSub = rec.OIDCSub
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":      user,
		"email":     email,
		"oidc_sub":  oidcSub,
		"is_admin":  a.Admins[user],
		"auth_mode": a.Config.Auth.Mode,
	})
}

// oidcLogin initiates the OIDC Authorization Code + PKCE redirect.
func (a *API) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if !a.oidcActive() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "OIDC authentication is disabled (set auth.oidc.enabled=true in server.json)",
		})
		return
	}
	disc, err := a.OIDC.discover(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	state, err := randomBase64URL(24)
	if err != nil {
		internalError(w, r, err)
		return
	}
	nonce, err := randomBase64URL(24)
	if err != nil {
		internalError(w, r, err)
		return
	}
	verifier, err := randomBase64URL(32)
	if err != nil {
		internalError(w, r, err)
		return
	}
	deviceCode := strings.TrimSpace(r.URL.Query().Get("device_code"))

	now := time.Now()
	a.OIDC.mu.Lock()
	a.OIDC.purgeLocked(now)
	a.OIDC.states[state] = oidcPendingState{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: verifier,
		DeviceCode:   deviceCode,
		Expires:      now.Add(10 * time.Minute),
	}
	a.OIDC.mu.Unlock()

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", a.OIDC.cfg.ClientID)
	q.Set("redirect_uri", a.OIDC.cfg.RedirectURI)
	q.Set("scope", strings.Join(a.OIDC.cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", pkceS256(verifier))
	q.Set("code_challenge_method", "S256")

	http.Redirect(w, r, disc.AuthorizationEndpoint+"?"+q.Encode(), http.StatusFound)
}

// oidcCallback handles GET /api/v1/auth/oidc/callback and GET /oidc.
func (a *API) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if !a.oidcActive() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "OIDC authentication is disabled (set auth.oidc.enabled=true in server.json)",
		})
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":       errParam,
			"description": r.URL.Query().Get("error_description"),
		})
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing code or state"})
		return
	}

	now := time.Now()
	a.OIDC.mu.Lock()
	a.OIDC.purgeLocked(now)
	pending, ok := a.OIDC.states[state]
	if ok {
		delete(a.OIDC.states, state)
	}
	a.OIDC.mu.Unlock()
	if !ok || now.After(pending.Expires) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid or expired OIDC state"})
		return
	}

	disc, err := a.OIDC.discover(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", a.OIDC.cfg.RedirectURI)
	form.Set("client_id", a.OIDC.cfg.ClientID)
	form.Set("code_verifier", pending.CodeVerifier)
	if secret := a.OIDC.cfg.ResolvedClientSecret(); secret != "" {
		form.Set("client_secret", secret)
	}

	tokenReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, disc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		internalError(w, r, err)
		return
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenReq.Header.Set("Accept", "application/json")

	tokenResp, err := a.OIDC.httpClient.Do(tokenReq)
	if err != nil {
		internalError(w, r, fmt.Errorf("oidc token exchange: %w", err))
		return
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(tokenResp.Body, 4096))
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": fmt.Sprintf("oidc token endpoint returned %d: %s", tokenResp.StatusCode, strings.TrimSpace(string(raw))),
		})
		return
	}

	var tokenPayload struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if err := json.NewDecoder(io.LimitReader(tokenResp.Body, 1<<20)).Decode(&tokenPayload); err != nil {
		internalError(w, r, fmt.Errorf("oidc token decode: %w", err))
		return
	}
	if tokenPayload.IDToken == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "oidc response missing id_token"})
		return
	}

	claims, err := a.OIDC.verifyIDToken(r.Context(), tokenPayload.IDToken, pending.Nonce)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid id_token: " + err.Error()})
		return
	}

	user := claimString(claims, a.OIDC.cfg.RemoteUserClaim)
	email := claimString(claims, a.OIDC.cfg.EmailClaim)
	sub := claimString(claims, "sub")

	if (user == "" || email == "") && disc.UserinfoEndpoint != "" && tokenPayload.AccessToken != "" {
		if ui, err := a.OIDC.fetchUserinfo(r.Context(), disc.UserinfoEndpoint, tokenPayload.AccessToken); err == nil {
			if user == "" {
				user = claimString(ui, a.OIDC.cfg.RemoteUserClaim)
			}
			if email == "" {
				email = claimString(ui, a.OIDC.cfg.EmailClaim)
			}
			for k, v := range ui {
				if _, exists := claims[k]; !exists {
					claims[k] = v
				}
			}
		}
	}
	if user == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": fmt.Sprintf("oidc token missing remote_user_claim %q", a.OIDC.cfg.RemoteUserClaim),
		})
		return
	}

	if len(a.OIDC.cfg.AllowedGroups) > 0 && !groupAllowed(claims[a.OIDC.cfg.GroupsClaim], a.OIDC.cfg.AllowedGroups) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "user is not in an allowed OIDC group"})
		return
	}

	sessionToken, err := a.completeUserLogin(user, email, sub, claims)
	if err != nil {
		internalError(w, r, err)
		return
	}

	if pending.DeviceCode != "" {
		a.OIDC.mu.Lock()
		if dev, ok := a.OIDC.devices[pending.DeviceCode]; ok && time.Now().Before(dev.Expires) {
			dev.Approved = true
			dev.Token = sessionToken
			dev.User = user
			dev.Email = email
		}
		a.OIDC.mu.Unlock()
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") || r.URL.Query().Get("format") == "json" {
		writeJSON(w, http.StatusOK, map[string]any{
			"token": sessionToken,
			"user":  user,
			"email": email,
		})
		return
	}
	if pending.DeviceCode != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "<!doctype html><html><body style=\"font-family:sans-serif;padding:2rem\"><h2>DeepThought CLI Login Approved</h2><p>Signed in as <strong>%s</strong> (%s). You can return to your terminal now.</p></body></html>", user, email)
		return
	}
	http.Redirect(w, r, "/#token="+url.QueryEscape(sessionToken)+"&user="+url.QueryEscape(user), http.StatusFound)
}

func (p *OIDCProvider) fetchUserinfo(ctx context.Context, endpoint, accessToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo HTTP %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func claimString(claims map[string]any, key string) string {
	if claims == nil || key == "" {
		return ""
	}
	v, _ := claims[key].(string)
	return strings.TrimSpace(v)
}

func groupAllowed(rawGroups any, allowed []string) bool {
	want := map[string]bool{}
	for _, g := range allowed {
		want[strings.TrimSpace(g)] = true
	}
	switch v := rawGroups.(type) {
	case string:
		return want[v]
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && want[s] {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if want[s] {
				return true
			}
		}
	}
	return false
}

func (a *API) completeUserLogin(user, email, sub string, claims map[string]any) (string, error) {
	token := a.Sessions.Login(user)
	if token == "" {
		return "", errors.New("session token generation failed")
	}
	if a.Users != nil {
		if err := a.Users.UpsertLoginWithIdentity(sanitizeUserID(user), user, email, sub); err != nil {
			return "", fmt.Errorf("user record: %w", err)
		}
	}
	a.runPostLoginHook(user, email, claims)
	return token, nil
}

// runPostLoginHook executes the OOD-compatible post-login hook
// (`post_login_hook --user <username>`) with OIDC_CLAIM_email and configured
// claim exports in the environment.
func (a *API) runPostLoginHook(user, email string, claims map[string]any) {
	hook := strings.TrimSpace(a.Config.Auth.OIDC.PostLoginHook)
	if hook == "" {
		return
	}
	if _, err := os.Stat(hook); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, hook, "--user", user)
	env := os.Environ()
	if email != "" {
		env = append(env, "OIDC_CLAIM_email="+email, "OOD_OIDC_CLAIM_email="+email)
	}
	env = append(env, "OIDC_CLAIM_preferred_username="+user)
	for _, exp := range a.Config.Auth.OIDC.PostLoginHookExports {
		claimName := strings.TrimPrefix(exp, "OIDC_CLAIM_")
		if val := claimString(claims, claimName); val != "" {
			env = append(env, exp+"="+val)
		}
	}
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("oidc post_login_hook (%s): %v (%s)", hook, err, strings.TrimSpace(string(out)))
	}
}

func (a *API) ensureOIDC() *OIDCProvider {
	if a.OIDC == nil {
		a.OIDC = NewOIDCProvider(a.Config.Auth.OIDC)
	}
	return a.OIDC
}

// deviceStartHandler handles POST /api/v1/auth/device/start for CLI browser-link login
// (supports both DRAC OIDC redirect and Web UI one-click approval).
func (a *API) deviceStartHandler(w http.ResponseWriter, r *http.Request) {
	if !a.anyAuthConfigured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "server authentication is not configured",
		})
		return
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		internalError(w, r, err)
		return
	}
	deviceCode := hex.EncodeToString(b)
	userCode := strings.ToUpper(deviceCode[:4] + "-" + deviceCode[4:8])
	now := time.Now()
	prov := a.ensureOIDC()
	prov.mu.Lock()
	prov.purgeLocked(now)
	prov.devices[deviceCode] = &oidcDeviceTicket{
		DeviceCode: deviceCode,
		UserCode:   userCode,
		Expires:    now.Add(10 * time.Minute),
	}
	prov.mu.Unlock()

	verifyURI := "/?device_code=" + url.QueryEscape(deviceCode) + "&user_code=" + url.QueryEscape(userCode)
	if a.oidcActive() {
		verifyURI = "/api/v1/auth/oidc/login?device_code=" + url.QueryEscape(deviceCode)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":      deviceCode,
		"user_code":        userCode,
		"verification_uri": verifyURI,
		"auth_mode":        a.Config.Auth.Mode,
		"oidc_enabled":     a.oidcActive(),
		"expires_in":       600,
		"interval":         2,
	})
}

// deviceApproveHandler handles POST /api/v1/auth/device/approve so a user signed into
// the Web UI (or providing their username + server password) can approve a CLI
// browser-link login in one click.
func (a *API) deviceApproveHandler(w http.ResponseWriter, r *http.Request) {
	if !a.anyAuthConfigured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "server authentication is not configured"})
		return
	}
	var req struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		User       string `json:"user"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	authedUser := ""
	if tok := bearer(r); tok != "" && a.Sessions != nil {
		if sess, ok := a.Sessions.Valid(tok); ok {
			authedUser = sess.User
		}
	}
	if authedUser == "" {
		user := strings.TrimSpace(req.User)
		if !a.passwordActive() || user == "" || !a.passwordMatches(req.Password) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized: sign in or provide valid user and password"})
			return
		}
		authedUser = user
	}

	devCode := strings.TrimSpace(req.DeviceCode)
	usrCode := strings.ToUpper(strings.TrimSpace(req.UserCode))
	if devCode == "" && usrCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device_code or user_code required"})
		return
	}

	now := time.Now()
	prov := a.ensureOIDC()
	prov.mu.Lock()
	prov.purgeLocked(now)
	var ticket *oidcDeviceTicket
	if devCode != "" {
		ticket = prov.devices[devCode]
	}
	if ticket == nil && usrCode != "" {
		for _, t := range prov.devices {
			if strings.EqualFold(t.UserCode, usrCode) {
				ticket = t
				break
			}
		}
	}
	if ticket == nil || now.After(ticket.Expires) {
		prov.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "expired or unknown device_code"})
		return
	}
	prov.mu.Unlock()

	sessionToken, err := a.completeUserLogin(authedUser, "", "", nil)
	if err != nil {
		internalError(w, r, err)
		return
	}

	prov.mu.Lock()
	ticket.Approved = true
	ticket.Token = sessionToken
	ticket.User = authedUser
	prov.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "approved",
		"user":      authedUser,
		"user_code": ticket.UserCode,
	})
}

// devicePollHandler handles POST /api/v1/auth/device/poll.
func (a *API) devicePollHandler(w http.ResponseWriter, r *http.Request) {
	if !a.anyAuthConfigured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "server authentication is not configured",
		})
		return
	}
	var req struct {
		DeviceCode string `json:"device_code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.DeviceCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device_code required"})
		return
	}
	now := time.Now()
	prov := a.ensureOIDC()
	prov.mu.Lock()
	prov.purgeLocked(now)
	ticket, ok := prov.devices[req.DeviceCode]
	if ok && ticket.Approved {
		delete(prov.devices, req.DeviceCode)
	}
	prov.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "expired or unknown device_code"})
		return
	}
	if !ticket.Approved {
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "authorization_pending"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "approved",
		"token":  ticket.Token,
		"user":   ticket.User,
		"email":  ticket.Email,
	})
}
