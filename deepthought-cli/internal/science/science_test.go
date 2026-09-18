package science

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"annorax/internal/config"
)

func TestFictionalToolServerNeedsNoCodeChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/forecast" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"forecast":42}`))
	}))
	defer server.Close()
	manifest := filepath.Join(t.TempDir(), "models.json")
	raw := `{"models":[{
		"id":"fictional_forecast",
		"description":"Forecast a fictional system",
		"endpoint":"/v1/forecast",
		"input_schema":{"type":"object","properties":{"series":{"type":"array"}},"required":["series"]},
		"capabilities":["predict"],
		"always_up":true
	}]}`
	if err := os.WriteFile(manifest, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := config.Provider{
		Name: "Fictional", BaseURL: server.URL, APIKey: "token",
		Kind: "tool_server", CatalogSource: string(CatalogSkill),
	}
	cards, err := (ToolServer{Provider: provider, Manifest: manifest}).Discover(context.Background())
	if err != nil || len(cards) != 1 {
		t.Fatalf("discover=%+v err=%v", cards, err)
	}
	tool, err := Compile(provider, cards[0], server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result := tool.Run(context.Background(), map[string]any{"series": []int{1, 2}})
	if result.IsError || !strings.Contains(result.Content, "42") {
		t.Fatalf("result=%+v", result)
	}
	if len(HotPath(cards)) != 1 {
		t.Fatal("always-up model missing from hot path")
	}
}

func TestArtifactTransitiveStaleness(t *testing.T) {
	a := &Artifact{ID: "a"}
	b := &Artifact{ID: "b", Consumes: []string{"a"}}
	c := &Artifact{ID: "c", Consumes: []string{"b"}}
	graph := map[string]*Artifact{"a": a, "b": b, "c": c}
	stale, err := StaleClosure(graph, "a")
	if err != nil || len(stale) != 2 || !b.Stale || !c.Stale {
		t.Fatalf("stale=%v err=%v", stale, err)
	}
}
