package science

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/unimatrix"
)

type CatalogSource string

const (
	CatalogOpenAI  CatalogSource = "openai_models"
	CatalogRich    CatalogSource = "rich_manifest"
	CatalogOpenAPI CatalogSource = "openapi"
	CatalogSkill   CatalogSource = "skill_manifest"
)

type ModelCard struct {
	ID           string                 `json:"id"`
	Description  string                 `json:"description,omitempty"`
	Endpoint     string                 `json:"endpoint,omitempty"`
	Method       string                 `json:"method,omitempty"`
	InputSchema  map[string]any         `json:"input_schema,omitempty"`
	OutputSchema map[string]any         `json:"output_schema,omitempty"`
	Capabilities []unimatrix.Capability `json:"capabilities,omitempty"`
	AlwaysUp     bool                   `json:"always_up,omitempty"`
	TimeoutMS    int                    `json:"timeout_ms,omitempty"`
}

type ToolServer struct {
	Provider config.Provider
	Client   *http.Client
	Manifest string // file path for skill_manifest, URL path for rich/openapi
}

func (s ToolServer) Discover(ctx context.Context) ([]ModelCard, error) {
	source := CatalogSource(s.Provider.CatalogSource)
	switch source {
	case CatalogSkill:
		raw, err := os.ReadFile(s.Manifest)
		if err != nil {
			return nil, err
		}
		return decodeCards(raw)
	case CatalogOpenAI:
		raw, err := s.get(ctx, "/models")
		if err != nil {
			return nil, err
		}
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		cards := make([]ModelCard, 0, len(payload.Data))
		for _, model := range payload.Data {
			cards = append(cards, ModelCard{ID: model.ID})
		}
		return cards, nil
	case CatalogRich, CatalogOpenAPI:
		path := s.Manifest
		if path == "" {
			path = "/models"
		}
		raw, err := s.get(ctx, path)
		if err != nil {
			return nil, err
		}
		return decodeCards(raw)
	default:
		return nil, fmt.Errorf("science: unknown catalog source %q", source)
	}
}

func decodeCards(raw []byte) ([]ModelCard, error) {
	var direct struct {
		Models []ModelCard `json:"models"`
		Data   []ModelCard `json:"data"`
	}
	if err := json.Unmarshal(raw, &direct); err != nil {
		return nil, err
	}
	if len(direct.Models) > 0 {
		return direct.Models, nil
	}
	return direct.Data, nil
}

func (s ToolServer) get(ctx context.Context, path string) ([]byte, error) {
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.Provider.BaseURL, "/")+"/"+strings.TrimLeft(path, "/"), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+s.Provider.ExpandedKey())
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("science: catalog %s: %s", response.Status, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}
