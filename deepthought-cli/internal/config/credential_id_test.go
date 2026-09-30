package config

import "testing"

// Same vectors as deepthought-server/server/vault_test.go.
func TestCredentialIdentityVectors(t *testing.T) {
	if got := CredentialID("Aleph", "https://aleph.example/v1/", "OpenAI"); got != "97366b05016442f6c338051755868ccc" {
		t.Fatalf("CredentialID = %s", got)
	}
	if got := CredentialFingerprint(make([]byte, 32), "sk-test"); got != "3e84892378bf40a45a7f6ed412f53d0bfd5711037d8775965219bbdc32555ed0" {
		t.Fatalf("CredentialFingerprint = %s", got)
	}
}

func TestSyncCredentialStaysLocal(t *testing.T) {
	off := false
	f := File{Providers: []Provider{{Name: "A", BaseURL: "https://a/v1", Wire: "openai", APIKey: "$A_KEY", SyncCredential: &off}}}
	doc := Shared(f)
	for _, p := range doc["providers"].([]any) {
		if _, ok := p.(map[string]any)["sync_credential"]; ok {
			t.Fatal("sync_credential leaked into the shared document")
		}
	}
	if Portable(f).Providers[0].SyncCredential != nil {
		t.Fatal("Portable kept sync_credential")
	}
	if f.Providers[0].SyncsCredential() {
		t.Fatal("opt-out ignored")
	}
}
