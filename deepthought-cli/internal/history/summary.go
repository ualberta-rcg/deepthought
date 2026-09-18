package history

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"annorax/internal/babel"
)

// Default condensing limits. These are intentionally conservative; they produce
// a short preview that is cheaper to send than Full but still human-readable.
const (
	DefaultCondenseMaxLines = 20
	DefaultCondenseMaxChars = 2000
)

// Default summary model: small, fast, and good enough for one-to-two-sentence
// summaries. Used when the config omits summary_model.
const DefaultSummaryModelID = "gpt-oss-20b"

// ModelRef identifies a model+provider. ProviderID is a future hook; today only
// the configured OpenAI-compatible provider is used.
type ModelRef struct {
	ProviderID string // e.g. "openai", "anthropic"; empty means default provider
	ModelID    string // catalog wire ID, e.g. "gpt-oss-20b"
}

// SummaryEngine provides built-in summary functions. None of them are invoked
// automatically during chat; external summarization passes call them explicitly.
type SummaryEngine struct {
	client     *babel.Client
	defaultRef ModelRef
}

// NewSummaryEngine builds an engine for one model (typically the summary
// role's). modelID empty falls back to gpt-oss-20b: small, fast, and good
// enough for one-to-two-sentence summaries.
func NewSummaryEngine(client *babel.Client, modelID string) *SummaryEngine {
	if modelID == "" {
		modelID = DefaultSummaryModelID
	}
	return &SummaryEngine{
		client:     client,
		defaultRef: ModelRef{ModelID: modelID},
	}
}

// ModelRef returns the engine's default model reference.
func (e *SummaryEngine) ModelRef() ModelRef { return e.defaultRef }

// Condense produces a condensed summary of text: first few lines, truncated to
// maxChars. If text is already short, it is returned unchanged.
func (e *SummaryEngine) Condense(text string, maxLines, maxChars int) string {
	return Condense(text, maxLines, maxChars)
}

// Tombstone returns a default tombstone marker for an object kind and ID.
func (e *SummaryEngine) Tombstone(kind, id string) string {
	return TombstoneFor(kind, id)
}

// Semantic asks the configured model for a one-to-two-sentence summary.
func (e *SummaryEngine) Semantic(ctx context.Context, text string) (string, error) {
	return e.runPrompt(ctx, "Summarize the following text in one or two sentences. Preserve facts the user or agent might need later.", text, 128)
}

// Bullets asks the configured model for a short bullet list.
func (e *SummaryEngine) Bullets(ctx context.Context, text string, count int) (string, error) {
	if count <= 0 {
		count = 3
	}
	prompt := fmt.Sprintf("Summarize the following text as %d concise bullets.", count)
	return e.runPrompt(ctx, prompt, text, 256)
}

// ExtractPatterns asks the configured model to extract durable facts from a
// probe result and returns them as Pattern objects linked to the probe.
func (e *SummaryEngine) ExtractPatterns(ctx context.Context, probe *Probe) ([]*Pattern, error) {
	if probe == nil || probe.Result.Content == "" {
		return nil, nil
	}
	prompt := `Extract durable facts from the following tool result. Return a JSON array of objects with fields "category" and "content". If there are none, return [].`
	reply, err := e.runPrompt(ctx, prompt, probe.Result.Content, 512)
	if err != nil {
		return nil, err
	}
	reply = strings.TrimSpace(reply)
	if reply == "" || reply == "[]" {
		return nil, nil
	}
	var raw []struct {
		Category string `json:"category"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(reply), &raw); err != nil {
		return nil, fmt.Errorf("summary engine: unmarshal patterns: %w", err)
	}
	var out []*Pattern
	for _, r := range raw {
		if r.Content == "" {
			continue
		}
		category := r.Category
		if category == "" {
			category = inferCategory(probe.Name, r.Content)
		}
		pattern := newPattern(probe, category, r.Content)
		out = append(out, pattern)
	}
	return out, nil
}

// WriteSummarySet populates all summary levels on set from text. It is the
// explicit entry point external summarization passes should use.
func (e *SummaryEngine) WriteSummarySet(ctx context.Context, set *SummarySet, text string) error {
	if set == nil {
		return nil
	}
	set.Full = text
	set.Condensed = e.Condense(text, 0, 0)
	set.Tombstone = e.Tombstone("object", "")
	if e != nil && e.client != nil {
		if semantic, err := e.Semantic(ctx, text); err == nil {
			set.Semantic = semantic
		}
	}
	return nil
}

// runPrompt is the common non-streaming call used by all model-backed summaries.
func (e *SummaryEngine) runPrompt(ctx context.Context, systemPrompt, text string, maxTokens int) (string, error) {
	if text == "" {
		return "", nil
	}
	trimmed := text
	if len(trimmed) > 4000 {
		trimmed = trimmed[:4000] + "\n..."
	}
	req := babel.ChatRequest{
		Model: e.defaultRef.ModelID,
		Messages: []babel.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: trimmed},
		},
		MaxTokens:   maxTokens,
		Temperature: 0.3,
	}
	reply, err := e.client.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("summary engine: %w", err)
	}
	return strings.TrimSpace(reply.Text), nil
}

// newPattern is a helper that builds a Pattern object linked to its source probe.
func newPattern(probe *Probe, category, content string) *Pattern {
	pattern := &Pattern{
		Vinculum: Vinculum{
			ID:           newID("pattern"),
			Kind:         "pattern",
			SessionID:    probe.SessionID,
			CollectiveID: probe.CollectiveID,
			PlanID:       probe.PlanID,
			ParentID:     probe.ID,
			CreatedAt:    now(),
		},
		Category:       category,
		Content:        content,
		SourceProbeID:  probe.ID,
		SourceToolName: probe.Name,
	}
	pattern.LinkTo(probe, "extracted_from")
	return pattern
}

// now is a test seam: it returns the current time. Tests can patch it.
var now = func() time.Time { return time.Now() }

// Condense produces a condensed summary of text: first few lines, truncated to
// maxChars. If text is already short, it is returned unchanged.
func Condense(text string, maxLines, maxChars int) string {
	if text == "" {
		return ""
	}
	if maxLines <= 0 {
		maxLines = DefaultCondenseMaxLines
	}
	if maxChars <= 0 {
		maxChars = DefaultCondenseMaxChars
	}

	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, "...")
	}
	out := strings.Join(lines, "\n")
	return truncateString(out, maxChars)
}

// TombstoneFor returns a default tombstone marker for an object kind and ID.
func TombstoneFor(kind, id string) string {
	return "[" + kind + " " + id + " summarized]"
}

// truncateString truncates s to max runes, appending "..." if truncated.
func truncateString(s string, max int) string {
	if max <= 0 {
		return s
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	// Leave room for the ellipsis.
	runes := []rune(s)
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
