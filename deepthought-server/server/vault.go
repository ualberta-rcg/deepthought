package server

// The credential vault: AES-256-GCM under a server key-encryption key from
// $DEEPTHOUGHT_VAULT_KEY (32 random bytes, hex or base64, held in a
// Kubernetes secret). Each row gets a fresh nonce; the additional data binds
// a ciphertext to its user and provider so rows cannot be swapped.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Vault seals and opens credential values.
type Vault struct{ aead cipher.AEAD }

// NewVault parses a 32-byte key given as hex or base64 (standard or URL).
func NewVault(key string) (*Vault, error) {
	key = strings.TrimSpace(key)
	var raw []byte
	for _, decode := range []func(string) ([]byte, error){
		hex.DecodeString,
		base64.StdEncoding.DecodeString,
		func(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")) },
	} {
		if b, err := decode(key); err == nil && len(b) == 32 {
			raw = b
			break
		}
	}
	if raw == nil {
		return nil, errors.New("vault key must be 32 bytes, hex or base64 encoded")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	return &Vault{aead: aead}, nil
}

func vaultAAD(userID, credentialID string) []byte {
	return []byte(userID + "|" + credentialID)
}

// Seal encrypts value for (userID, credentialID).
func (v *Vault) Seal(userID, credentialID, value string) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, v.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, v.aead.Seal(nil, nonce, []byte(value), vaultAAD(userID, credentialID)), nil
}

// Open decrypts a value sealed for (userID, credentialID).
func (v *Vault) Open(userID, credentialID string, nonce, ciphertext []byte) (string, error) {
	if len(nonce) != v.aead.NonceSize() {
		return "", errors.New("vault: bad nonce")
	}
	plain, err := v.aead.Open(nil, nonce, ciphertext, vaultAAD(userID, credentialID))
	if err != nil {
		return "", errors.New("vault: cannot decrypt (wrong key or tampered row)")
	}
	return string(plain), nil
}

// Fingerprint is HMAC-SHA256(salt, value) in hex. The client computes the
// same with the salt from GET /user/credentials.
func Fingerprint(salt []byte, value string) string {
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}

// CredentialID is the provider identity key: the first 32 hex digits of
// SHA-256("name\nbase_url\nwire") with the name and wire lower-cased and
// trailing slashes dropped from the URL. The CLI has an identical copy
// (internal/config); a shared test vector keeps them in step.
func CredentialID(name, baseURL, wire string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name)) + "\n" +
		strings.TrimRight(strings.TrimSpace(baseURL), "/") + "\n" +
		strings.ToLower(strings.TrimSpace(wire))))
	return hex.EncodeToString(sum[:16])
}
