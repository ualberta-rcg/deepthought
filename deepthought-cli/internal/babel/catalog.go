package babel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CatalogEntry struct {
	ID            string          `json:"id"`
	Type          string          `json:"type,omitempty"`
	Context       int             `json:"context_window,omitempty"`
	MaxCompletion int             `json:"max_completion_tokens,omitempty"`
	Capabilities  map[string]bool `json:"capabilities,omitempty"`
}

func (c *Client) ListCatalog(ctx context.Context) ([]CatalogEntry, error) {
	if c.APIKey == "" && !c.AllowAnonymous {
		return nil, fmt.Errorf("provider credential is missing; configure it in Settings")
	}
	endpoint := strings.TrimRight(c.BaseURL, "/")
	if c.Wire == "anthropic" && !strings.HasSuffix(endpoint, "/v1") {
		endpoint += "/v1"
	}
	endpoint += "/models"
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid model catalog URL")
	}
	if parsed.Host == "inference.vulcan.alliancecan.ca" {
		endpoint = "https://inference.vulcan.alliancecan.ca/v1/models"
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid model catalog request")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.Wire == "anthropic" && parsed.Host != "inference.vulcan.alliancecan.ca" {
		req.Header.Set("x-api-key", c.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	client := *c.HTTP
	origin, _ := url.Parse(endpoint)
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != origin.Scheme || r.URL.Host != origin.Host {
			return fmt.Errorf("catalog redirect refused")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable; check the endpoint or retry")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog returned HTTP %d; check credentials, endpoint, or enter a model ID manually", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, fmt.Errorf("catalog response too large or incomplete")
	}
	var result struct {
		Data []CatalogEntry `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, fmt.Errorf("provider returned an unsupported model catalog")
	}
	if len(result.Data) > 5000 {
		return nil, fmt.Errorf("catalog contains too many models")
	}
	out := []CatalogEntry{}
	for _, e := range result.Data {
		if e.ID != "" && len(e.ID) <= 512 && !strings.ContainsAny(e.ID, "\x1b\n\r\x00") {
			out = append(out, e)
		}
	}
	return out, nil
}
