package server

// Credential sync endpoints (/api/v1/user/credentials) and session
// management (/api/v1/auth/logout, /api/v1/user/sessions). Credential values
// are only ever returned by GET /user/credentials/{id} to the owning session;
// the index carries fingerprints. Request logging stays method/path/duration.
//
// Trust assumption (docs/DEPLOYMENT.md): with the phase-1 shared password,
// anyone holding it can log in under any username and read that user's
// credentials. Credential sync is only for deployments where every password
// holder is trusted with every stored key.

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"deepthought-server/store"
)

const maxCredentialValue = 8 << 10

var (
	credentialIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)
	envRefShape       = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

func (a *API) credentialsReady(w http.ResponseWriter) bool {
	if a.Credentials == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": errDBNotConfigured.Error()})
		return false
	}
	return true
}

// listCredentials returns the caller's credential index and fingerprint salt.
func (a *API) listCredentials(w http.ResponseWriter, r *http.Request) {
	if !a.credentialsReady(w) {
		return
	}
	user := sanitizeUserID(SessionUser(r))
	salt, err := a.Credentials.Salt(user)
	if err != nil {
		internalError(w, r, err)
		return
	}
	rows, err := a.Credentials.List(user)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"salt": hex.EncodeToString(salt), "vault": a.Vault != nil, "credentials": rows})
}

// getCredential returns one credential with its value.
func (a *API) getCredential(w http.ResponseWriter, r *http.Request) {
	if !a.credentialsReady(w) {
		return
	}
	id := r.PathValue("id")
	user := sanitizeUserID(SessionUser(r))
	got, err := a.Credentials.Get(user, id)
	if errors.Is(err, store.ErrNoCredential) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such credential"})
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	value := got.EnvRef
	if got.Kind == store.CredentialSecret {
		if a.Vault == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "credential vault not configured (set DEEPTHOUGHT_VAULT_KEY)"})
			return
		}
		if value, err = a.Vault.Open(user, id, got.Nonce, got.Ciphertext); err != nil {
			internalError(w, r, err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"id": got.ID, "name": got.Name, "base_url": got.BaseURL, "wire": got.Wire, "kind": got.Kind, "fingerprint": got.Fingerprint, "value": value})
}

// putCredential stores one credential; the path id must match the body's
// provider identity.
func (a *API) putCredential(w http.ResponseWriter, r *http.Request) {
	if !a.credentialsReady(w) {
		return
	}
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
		return
	}
	var req struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Wire    string `json:"wire"`
		Kind    string `json:"kind"`
		Value   string `json:"value"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be {name, base_url, wire, kind, value}"})
		return
	}
	if !credentialIDShape.MatchString(id) || CredentialID(req.Name, req.BaseURL, req.Wire) != id {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "credential id does not match the provider identity"})
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 128 || len(req.BaseURL) > 512 || len(req.Wire) > 32 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name required; name, base_url or wire too long"})
		return
	}
	user := sanitizeUserID(SessionUser(r))
	rec := store.StoredCredential{CredentialRow: store.CredentialRow{ID: id, Name: req.Name, BaseURL: req.BaseURL, Wire: req.Wire, Kind: req.Kind}}
	switch req.Kind {
	case store.CredentialEnv:
		if !envRefShape.MatchString(req.Value) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "env credentials are a $VARIABLE reference"})
			return
		}
		rec.EnvRef = req.Value
	case store.CredentialSecret:
		if req.Value == "" || len(req.Value) > maxCredentialValue {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "secret value empty or too long"})
			return
		}
		if a.Vault == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "credential vault not configured (set DEEPTHOUGHT_VAULT_KEY)"})
			return
		}
		if rec.Nonce, rec.Ciphertext, err = a.Vault.Seal(user, id, req.Value); err != nil {
			internalError(w, r, err)
			return
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": `kind must be "env" or "secret"`})
		return
	}
	salt, err := a.Credentials.Salt(user)
	if err != nil {
		internalError(w, r, err)
		return
	}
	rec.Fingerprint = Fingerprint(salt, req.Value)
	if err := a.Credentials.Put(user, rec); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "id": id, "fingerprint": rec.Fingerprint})
}

// deleteCredential forgets one credential on the server.
func (a *API) deleteCredential(w http.ResponseWriter, r *http.Request) {
	if !a.credentialsReady(w) {
		return
	}
	ok, err := a.Credentials.Delete(sanitizeUserID(SessionUser(r)), r.PathValue("id"))
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such credential"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

// logout revokes the calling session.
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	a.Sessions.Revoke(bearer(r))
	writeJSON(w, http.StatusOK, map[string]any{"status": "logged out"})
}

// listSessions lists the caller's sessions, marking the current one.
func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Sessions.List(SessionUser(r))
	if err != nil {
		internalError(w, r, err)
		return
	}
	current := ""
	if sess, ok := r.Context().Value(sessionKey{}).(Session); ok {
		current = sess.ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": current, "sessions": rows})
}

// revokeSession drops one of the caller's sessions.
func (a *API) revokeSession(w http.ResponseWriter, r *http.Request) {
	ok, err := a.Sessions.RevokeID(SessionUser(r), r.PathValue("id"))
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "revoked"})
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
