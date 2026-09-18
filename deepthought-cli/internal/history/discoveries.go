package history

import (
	"strings"
	"time"
)

// Extractor turns a completed probe result into zero or more patterns.
type Extractor interface {
	Extract(probe *Probe) []*Pattern
}

// DefaultExtractor is the extractor used by Probe.ExtractPatterns.
var DefaultExtractor Extractor = &SimpleExtractor{}

// SimpleExtractor scans Result.Content for lines that start with common
// discovery markers. This is intentionally manual for v0; a future phase can
// swap in an LLM-based extractor without changing the object model.
type SimpleExtractor struct {
	// Markers defaults to a small set of prefixes if nil.
	Markers []string
}

var defaultMarkers = []string{
	"DISCOVERY:",
	"LEARNED:",
	"NOTE:",
	"IMPORTANT:",
}

// Extract implements Extractor.
func (e *SimpleExtractor) Extract(probe *Probe) []*Pattern {
	if probe == nil || probe.Result.Content == "" {
		return nil
	}
	markers := e.Markers
	if len(markers) == 0 {
		markers = defaultMarkers
	}

	var out []*Pattern
	lines := strings.Split(probe.Result.Content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		for _, m := range markers {
			if strings.HasPrefix(strings.ToUpper(line), strings.ToUpper(m)) {
				content := strings.TrimSpace(strings.TrimPrefix(line, m))
				if content != "" {
					pattern := &Pattern{
						Vinculum: Vinculum{
							ID:           newID("pattern"),
							Kind:         "pattern",
							SessionID:    probe.SessionID,
							CollectiveID: probe.CollectiveID,
							PlanID:       probe.PlanID,
							ParentID:     probe.ID,
							Index:        len(out),
							CreatedAt:    time.Now(),
						},
						Category:       inferCategory(probe.Name, content),
						Content:        content,
						SourceProbeID:  probe.ID,
						SourceToolName: probe.Name,
					}
					if len(out) > 0 {
						pattern.LinkAfter(out[len(out)-1])
					}
					pattern.LinkTo(probe, "extracted_from")
					out = append(out, pattern)
				}
				break
			}
		}
	}
	return out
}

// ExtractPatterns runs the default extractor and stores the results on the
// probe. It is safe to call multiple times; it replaces previous patterns.
func (p *Probe) ExtractPatterns() []*Pattern {
	p.Patterns = DefaultExtractor.Extract(p)
	return p.Patterns
}

// inferCategory makes a naive guess at the pattern category based on the tool
// and content. This is a placeholder for a smarter classifier later.
func inferCategory(toolName, content string) string {
	switch toolName {
	case "bash":
		lower := strings.ToLower(content)
		if strings.Contains(lower, "go ") || strings.Contains(lower, "module") {
			return "env"
		}
		return "observation"
	case "read":
		return "file"
	default:
		return "observation"
	}
}

// NoopExtractor returns nothing. Useful for tests or when extraction is off.
type NoopExtractor struct{}

// Extract implements Extractor.
func (NoopExtractor) Extract(_ *Probe) []*Pattern { return nil }
