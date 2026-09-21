package science

import (
	"context"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/queen"
	"deepthought-cli/internal/tools"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfiguredToolUsesPermissionAndInputOutputContracts(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("anonymous request had authorization")
		}
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"models":[{"id":"forecast","endpoint":"/run","input_schema":{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}}},"output_schema":{"type":"object","required":["value"]}}]}`))
			return
		}
		calls++
		w.Write([]byte(`{"wrong":42}`))
	}))
	defer srv.Close()
	ts, err := Configured(context.Background(), []config.Provider{{Name: "fixture", Kind: "tool_server", Anonymous: true, BaseURL: srv.URL, CatalogSource: "rich_manifest"}}, nil)
	if err != nil || len(ts) != 1 {
		t.Fatalf("tools=%v err=%v", ts, err)
	}
	if result := tools.Execute(context.Background(), ts[0], map[string]any{"count": 0}); !result.IsError || calls != 0 {
		t.Fatal("invalid input reached server")
	}
	gate := queen.NewGate(queen.AlwaysProceed)
	args := map[string]any{"count": 1}
	if gate.Decide(context.Background(), ts[0], args) != queen.Ask {
		t.Fatal("scientific call bypassed approval")
	}
	if result := tools.Execute(context.Background(), ts[0], args); !result.IsError || calls != 1 {
		t.Fatal("invalid output accepted")
	}
	if _, err := (ToolServer{Provider: config.Provider{CatalogSource: "openapi"}}).Discover(context.Background()); err == nil {
		t.Fatal("OpenAPI silently treated as another format")
	}
}
