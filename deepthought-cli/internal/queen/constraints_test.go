package queen

import (
	"context"
	"deepthought-cli/internal/tools"
	"testing"
)

func TestSkillRestrictionsNeverWidenTaskGrants(t *testing.T) {
	g := NewGate(AlwaysProceed)
	bash := tools.NewGuardedBash()
	defer bash.Close()
	args := map[string]any{"command": "pwd"}
	g.GrantTask("bash", args)
	g.Restrict([]string{"Read"})
	if g.Decide(context.Background(), bash, args) != Deny {
		t.Fatal("grant bypassed skill restriction")
	}
	g.ClearTaskGrants()
	if g.Decide(context.Background(), bash, args) == Deny {
		t.Fatal("restriction leaked into next turn")
	}
}
