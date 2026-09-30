package server

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

const testVaultKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestVaultRoundTripAndBinding(t *testing.T) {
	v, err := NewVault(testVaultKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	nonce, ct, err := v.Seal("alice", "id1", "sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ct), "sk-secret") {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := v.Open("alice", "id1", nonce, ct)
	if err != nil || got != "sk-secret" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := v.Open("bob", "id1", nonce, ct); err == nil {
		t.Fatal("a row moved to another user must not decrypt")
	}
	if _, err := v.Open("alice", "id2", nonce, ct); err == nil {
		t.Fatal("a row moved to another provider must not decrypt")
	}
	n2, _, _ := v.Seal("alice", "id1", "sk-secret")
	if string(n2) == string(nonce) {
		t.Fatal("nonces must be fresh per seal")
	}
}

func TestVaultKeyFormats(t *testing.T) {
	raw, _ := hex.DecodeString(testVaultKeyHex)
	for name, key := range map[string]string{
		"hex":       testVaultKeyHex,
		"base64":    base64.StdEncoding.EncodeToString(raw),
		"base64url": base64.RawURLEncoding.EncodeToString(raw),
	} {
		if _, err := NewVault(key); err != nil {
			t.Errorf("%s key rejected: %v", name, err)
		}
	}
	for _, bad := range []string{"", "short", testVaultKeyHex[:62], base64.StdEncoding.EncodeToString(raw[:16])} {
		if _, err := NewVault(bad); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
}

// The CLI carries the same vectors (internal/config credential id test).
func TestCredentialIdentityVectors(t *testing.T) {
	if got := CredentialID("Aleph", "https://aleph.example/v1/", "OpenAI"); got != "97366b05016442f6c338051755868ccc" {
		t.Fatalf("CredentialID = %s", got)
	}
	if got := Fingerprint(make([]byte, 32), "sk-test"); got != "3e84892378bf40a45a7f6ed412f53d0bfd5711037d8775965219bbdc32555ed0" {
		t.Fatalf("Fingerprint = %s", got)
	}
	if Fingerprint([]byte("a"), "x") == Fingerprint([]byte("b"), "x") {
		t.Fatal("fingerprint must depend on the salt")
	}
}
