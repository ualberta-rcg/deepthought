package babel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCatalogMetadataAndSafeErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("wrong catalog path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-catalog-canary" {
			t.Error("missing bearer credential")
		}
		fmt.Fprint(w, `{"data":[{"id":"unknown-name","type":"chat","context_window":4096,"capabilities":{"tools":true,"vision":false}},{"id":"gpt-oss-guessing-is-wrong"}]}`)
	}))
	defer server.Close()
	client := NewClientWithOptions(server.URL+"/v1", "synthetic-catalog-canary", "openai", 0)
	entries, err := client.ListCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !entries[0].Capabilities["tools"] || entries[0].Context != 4096 || entries[1].Capabilities != nil {
		t.Fatal("catalog metadata lost or invented")
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected requests")
	}
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "synthetic-catalog-canary")
	}))
	defer denied.Close()
	client.BaseURL = denied.URL
	_, err = client.ListCatalog(context.Background())
	if err == nil || strings.Contains(err.Error(), "synthetic-catalog-canary") {
		t.Fatal("unsafe provider error")
	}
}
func TestCatalogRefusesCredentialRedirect(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer origin.Close()
	client := NewClientWithOptions(origin.URL, "synthetic-redirect-canary", "openai", 0)
	if _, err := client.ListCatalog(context.Background()); err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
	if leaked.Load() {
		t.Fatal("contacted unapproved endpoint")
	}
}
