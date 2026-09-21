package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayerPrecedenceAndExplicitZero(t *testing.T) {
	server := map[string]any{"temperature": 0.9, "appearance": map[string]any{"top_bar_legend": true}}
	local := map[string]any{"temperature": 0.0, "appearance": map[string]any{"top_bar_legend": false}}
	cfg, err := ResolveLayers(server, local, map[string]any{"language": "fr"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SamplingTemperature() != 0 || cfg.TopBarLegendOn() || cfg.Source("temperature") != "local" || cfg.Source("language") != "session" {
		t.Fatalf("incorrect effective layers: %+v", cfg)
	}
	if server["temperature"] != 0.9 {
		t.Fatal("mutated server defaults")
	}
}
func TestLocalPatchDoesNotSaveMergedDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	before := Defaults().File
	after := before
	after.Language = "fr"
	if err := SaveLocalPatch(path, nil, before, after); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "providers") || !strings.Contains(string(raw), "language") {
		t.Fatalf("stored merged settings: %s", raw)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Language != "fr" || len(cfg.Providers) == 0 {
		t.Fatalf("reload: %v", err)
	}
}
func TestServerLayerCannotSupplyCredentials(t *testing.T) {
	_, err := ResolveLayers(map[string]any{"providers": []any{map[string]any{"api_key": "$REFERENCE"}}}, nil, nil)
	if err == nil {
		t.Fatal("accepted server credentials")
	}
}
