package science

import (
	"context"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/tools"
	"errors"
	"fmt"
)

// Configured compiles only explicitly configured tool-server manifests.
func Configured(ctx context.Context, providers []config.Provider, reserved []tools.Tool) ([]tools.Tool, error) {
	seen := map[string]bool{}
	for _, t := range reserved {
		seen[t.Name()] = true
	}
	var out []tools.Tool
	var errs []error
	for _, p := range providers {
		if p.Kind != "tool_server" {
			continue
		}
		cards, err := (ToolServer{Provider: p, Manifest: p.Manifest}).Discover(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
			continue
		}
		for _, card := range cards {
			t, err := Compile(p, card, nil)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			compiled := t.(*compiledTool)
			compiled.name = toolNameChars.ReplaceAllString(p.Name, "_") + "__" + compiled.name
			if seen[t.Name()] {
				errs = append(errs, fmt.Errorf("duplicate scientific tool %q", t.Name()))
				continue
			}
			seen[t.Name()] = true
			out = append(out, t)
		}
	}
	return out, errors.Join(errs...)
}
