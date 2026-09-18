package science

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/tools"
)

var toolNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func Compile(provider config.Provider, card ModelCard, client *http.Client) (tools.Tool, error) {
	if card.ID == "" {
		return nil, fmt.Errorf("science: model card has no id")
	}
	if card.Endpoint == "" || card.InputSchema == nil {
		return nil, fmt.Errorf("science: model %q needs endpoint and input_schema manifest data", card.ID)
	}
	if client == nil {
		timeout := 6 * time.Minute
		if card.TimeoutMS > 0 {
			timeout = time.Duration(card.TimeoutMS) * time.Millisecond
		}
		client = &http.Client{Timeout: timeout}
	}
	method := strings.ToUpper(card.Method)
	if method == "" {
		method = http.MethodPost
	}
	return &compiledTool{
		name: toolNameChars.ReplaceAllString(card.ID, "_"), description: card.Description,
		endpoint: strings.TrimRight(provider.BaseURL, "/") + "/" + strings.TrimLeft(card.Endpoint, "/"),
		method:   method, schema: card.InputSchema, key: provider.ExpandedKey(), client: client,
	}, nil
}

type compiledTool struct {
	name, description, endpoint, method, key string
	schema                                   map[string]any
	client                                   *http.Client
}

func (t *compiledTool) Name() string { return t.name }
func (t *compiledTool) Description() string {
	if t.description != "" {
		return t.description
	}
	return "Invoke scientific model " + t.name
}
func (t *compiledTool) Parameters() map[string]any { return t.schema }
func (*compiledTool) ReadOnly() bool               { return false }
func (t *compiledTool) Run(ctx context.Context, args map[string]any) tools.Result {
	raw, err := json.Marshal(args)
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error(), Summary: t.name + " · invalid input"}
	}
	request, err := http.NewRequestWithContext(ctx, t.method, t.endpoint, bytes.NewReader(raw))
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error(), Summary: t.name + " · request failed"}
	}
	request.Header.Set("Authorization", "Bearer "+t.key)
	request.Header.Set("Content-Type", "application/json")
	response, err := t.client.Do(request)
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error(), Summary: t.name + " · invoke failed"}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error(), Summary: t.name + " · read failed"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return tools.Result{IsError: true, Content: string(body), Summary: fmt.Sprintf("%s · HTTP %d", t.name, response.StatusCode)}
	}
	return tools.Result{Content: strings.TrimSpace(string(body)), Summary: t.name + " · complete"}
}

// HotPath returns only always-up cards suitable for embedding, reranking,
// classification, OCR, detection, and other latency-sensitive routing.
func HotPath(cards []ModelCard) []ModelCard {
	var out []ModelCard
	for _, card := range cards {
		if card.AlwaysUp {
			out = append(out, card)
		}
	}
	return out
}
