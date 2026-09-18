package app

import (
	"errors"
	"testing"

	"annorax/internal/config"
	"annorax/internal/history"
	"annorax/internal/unimatrix"
)

func TestRouteNeverRelaxesSensitivity(t *testing.T) {
	file := config.File{
		Providers: []config.Provider{
			{Name: "public", BaseURL: "https://public", APIKey: "x", Wire: "openai", Clearance: "public"},
			{Name: "internal", BaseURL: "https://internal", APIKey: "x", Wire: "openai", Clearance: "internal"},
		},
		Models: []unimatrix.Model{
			{ID: "a", Provider: "public", Capabilities: []unimatrix.Capability{unimatrix.CapGenerate}},
			{ID: "b", Provider: "internal", Capabilities: []unimatrix.Capability{unimatrix.CapGenerate}},
		},
		Roles:  map[string]string{"chat": "a"},
		Routes: map[string]config.Route{"code": {Capability: unimatrix.CapGenerate, Prefer: []string{"public", "internal"}}},
	}
	cfg, err := config.Validate(file)
	if err != nil {
		t.Fatal(err)
	}
	settings := NewSettings(cfg, t.TempDir()+"/config.json")
	client, model, err := settings.RouteClient("code", history.SensitivityInternal, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "b" || client.BaseURL != "https://internal" {
		t.Fatalf("routed to %s %s", model.ID, client.BaseURL)
	}
	if _, _, err := settings.RouteClient("code", history.SensitivitySecret, 1, 0); err == nil {
		t.Fatal("secret request silently downgraded")
	}
}

func TestProviderCircuitBreaker(t *testing.T) {
	file := config.File{
		Providers: []config.Provider{{Name: "p", BaseURL: "https://p", APIKey: "x", Wire: "openai", Clearance: "secret", MaxFailures: 1}},
		Models:    []unimatrix.Model{{ID: "m", Provider: "p", Capabilities: []unimatrix.Capability{unimatrix.CapGenerate}}},
		Roles:     map[string]string{"chat": "m"},
	}
	cfg, _ := config.Validate(file)
	settings := NewSettings(cfg, t.TempDir()+"/config.json")
	settings.ReportProviderResult("p", errors.New("down"))
	if _, _, err := settings.RouteClient("code", history.SensitivityPublic, 1, 0); err == nil {
		t.Fatal("open circuit was routed")
	}
}
