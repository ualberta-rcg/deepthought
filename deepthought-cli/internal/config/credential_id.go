package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// CredentialID is the provider identity used by the server credential store:
// the first 32 hex digits of SHA-256("name\nbase_url\nwire"), name and wire
// lower-cased, trailing slashes dropped. deepthought-server/server/vault.go
// has the identical function; both test the same vector.
func CredentialID(name, baseURL, wire string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name)) + "\n" +
		strings.TrimRight(strings.TrimSpace(baseURL), "/") + "\n" +
		strings.ToLower(strings.TrimSpace(wire))))
	return hex.EncodeToString(sum[:16])
}

// CredentialFingerprint is HMAC-SHA256(salt, value) in hex, comparable with
// the server's fingerprint without sending the value.
func CredentialFingerprint(salt []byte, value string) string {
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}

// WireOrDefault is the provider's wire format, "openai" when unset.
func (p Provider) WireOrDefault() string {
	if p.Wire == "" {
		return "openai"
	}
	return p.Wire
}

// SyncsCredential reports whether the key may be stored on the server.
func (p Provider) SyncsCredential() bool {
	return p.Kind != "tool_server" && !p.Anonymous && (p.SyncCredential == nil || *p.SyncCredential)
}
