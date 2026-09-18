package tools

import (
	"context"
	"strings"
	"testing"
)

func testSkillTool() *Skill {
	lookup := func(name string) (string, bool, error) {
		bodies := map[string]string{
			"alpha": "# Alpha skill body",
			"beta":  "# Beta skill body",
		}
		b, ok := bodies[name]
		return b, ok, nil
	}
	return NewSkillTool([]string{"beta", "alpha"}, lookup)
}

func TestSkillToolListsWhenNoName(t *testing.T) {
	r := testSkillTool().Run(context.Background(), map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Content)
	}
	if !strings.Contains(r.Content, "alpha") || !strings.Contains(r.Content, "beta") {
		t.Errorf("listing missing skills: %q", r.Content)
	}
	// names are sorted regardless of input order
	if strings.Index(r.Content, "alpha") > strings.Index(r.Content, "beta") {
		t.Errorf("names not sorted: %q", r.Content)
	}
}

func TestSkillToolLoadsBody(t *testing.T) {
	r := testSkillTool().Run(context.Background(), map[string]any{"name": "alpha"})
	if r.IsError || !strings.Contains(r.Content, "# Alpha skill body") {
		t.Errorf("body load failed: err=%v content=%q", r.IsError, r.Content)
	}
}

func TestSkillToolNotFound(t *testing.T) {
	r := testSkillTool().Run(context.Background(), map[string]any{"name": "gamma"})
	if !r.IsError {
		t.Errorf("expected error for unknown skill")
	}
	if !strings.Contains(r.Content, "No skill named") {
		t.Errorf("missing not-found message: %q", r.Content)
	}
}

func TestSkillToolMeta(t *testing.T) {
	tt := testSkillTool()
	if tt.Name() != "skill" {
		t.Errorf("Name = %q, want skill", tt.Name())
	}
	if !tt.ReadOnly() {
		t.Errorf("skill tool should be ReadOnly (auto-allowed)")
	}
}
