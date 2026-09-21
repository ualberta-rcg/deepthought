package babel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// InvokeJSON is the minimal adoption path for non-chat scientific models.
func (c *Client) InvokeJSON(ctx context.Context, path string, input any, output any) error {
	if c.Wire != "json" {
		return fmt.Errorf("babel: provider wire %q is not raw json", c.Wire)
	}
	if c.APIKey == "" && !c.AllowAnonymous {
		return fmt.Errorf("babel: empty API key")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	url := c.BaseURL
	if path != "" {
		url += "/" + strings.TrimLeft(path, "/")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if c.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxBody))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("babel: %s: %s", response.Status, snippet(body))
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("babel: parse json response: %w", err)
	}
	return nil
}
