// Package ragflow provides a per-user RAGFlow knowledge-base retrieval and
// dataset management tool. Users can configure their RAGFlow base URL, API key
// (or $RAGFLOW_API_KEY), and default dataset IDs in Settings / Config.
package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/tools"
)

// Tool exposes RAGFlow knowledge retrieval and dataset listing to the agent.
type Tool struct {
	ConfigSource func() config.Config
	HTTP         *http.Client
}

func (*Tool) Name() string   { return "ragflow" }
func (*Tool) ReadOnly() bool { return true }
func (*Tool) Description() string {
	return "Query the user's configured RAGFlow knowledge bases (semantic + keyword retrieval over papers, cluster docs, lab notebooks, and project assets) or list datasets."
}

func (*Tool) Parameters() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type":                 "object",
		"required":             []string{"operation"},
		"additionalProperties": false,
		"properties": map[string]any{
			"operation":   map[string]any{"type": "string", "enum": []string{"retrieve", "datasets", "status"}},
			"question":    str,
			"dataset_ids": map[string]any{"type": "array", "items": str},
			"top_k":       map[string]any{"type": "integer"},
		},
	}
}

func (t *Tool) resolveConfig() (baseURL, apiKey string, datasets []string) {
	if t != nil && t.ConfigSource != nil {
		cfg := t.ConfigSource()
		if cfg.RAGFlow != nil {
			baseURL = strings.TrimSpace(cfg.RAGFlow.BaseURL)
			apiKey = strings.TrimSpace(cfg.RAGFlow.APIKey)
			datasets = cfg.RAGFlow.DatasetIDs
		}
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("RAGFLOW_BASE_URL"))
	}
	if strings.HasPrefix(apiKey, "$") {
		apiKey = strings.TrimSpace(os.Getenv(strings.TrimPrefix(apiKey, "$")))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("RAGFLOW_API_KEY"))
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return baseURL, apiKey, datasets
}

func (t *Tool) client() *http.Client {
	if t != nil && t.HTTP != nil {
		return t.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (t *Tool) Run(ctx context.Context, args map[string]any) tools.Result {
	op, _ := args["operation"].(string)
	baseURL, apiKey, defaultDatasets := t.resolveConfig()
	if op == "status" {
		status := map[string]any{
			"configured":  baseURL != "" && apiKey != "",
			"base_url":    baseURL,
			"dataset_ids": defaultDatasets,
		}
		raw, _ := json.MarshalIndent(status, "", "  ")
		return tools.Result{Content: string(raw), Summary: "ragflow · status"}
	}
	if baseURL == "" || apiKey == "" {
		return tools.Result{
			IsError: true,
			Content: "RAGFlow is not configured yet. Set ragflow.base_url and ragflow.api_key (or $RAGFLOW_API_KEY / $RAGFLOW_BASE_URL) via /rag or Settings.",
			Summary: "ragflow · unconfigured",
		}
	}

	switch op {
	case "datasets":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/datasets?page=1&page_size=50", nil)
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "ragflow · error"}
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := t.client().Do(req)
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "ragflow · request failed"}
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode >= 300 {
			return tools.Result{IsError: true, Content: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)), Summary: "ragflow · HTTP error"}
		}
		return tools.Result{Content: string(body), Summary: "ragflow · datasets"}

	case "retrieve":
		question, _ := args["question"].(string)
		if strings.TrimSpace(question) == "" {
			return tools.Result{IsError: true, Content: "question is required for retrieve", Summary: "ragflow · missing question"}
		}
		var dsIDs []string
		if rawIDs, ok := args["dataset_ids"].([]any); ok {
			for _, item := range rawIDs {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					dsIDs = append(dsIDs, strings.TrimSpace(s))
				}
			}
		}
		if len(dsIDs) == 0 {
			dsIDs = defaultDatasets
		}
		topK := 6
		if k, ok := args["top_k"].(float64); ok && int(k) > 0 {
			topK = int(k)
		}
		payload := map[string]any{
			"question":    question,
			"dataset_ids": dsIDs,
			"top_k":       topK,
		}
		raw, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/retrieval", bytes.NewReader(raw))
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "ragflow · error"}
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := t.client().Do(req)
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "ragflow · request failed"}
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 128<<10))
		if resp.StatusCode >= 300 {
			return tools.Result{IsError: true, Content: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)), Summary: "ragflow · HTTP error"}
		}
		return tools.Result{Content: string(body), Summary: fmt.Sprintf("ragflow · retrieve %q", question)}
	default:
		return tools.Result{IsError: true, Content: "unknown ragflow operation: " + op, Summary: "ragflow · invalid op"}
	}
}
