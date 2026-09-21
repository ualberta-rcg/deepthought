package queen

import (
	"context"
	"deepthought-cli/internal/tools"
	"testing"
)

func TestExplicitDenyOverridesGrant(t *testing.T) {
	g := NewGateFromConfig("safe", Rules{Deny: []string{"Bash(git *)"}}, nil)
	args := map[string]any{"command": "git status"}
	g.GrantAlways("bash", args)
	if g.Decide(context.Background(), tools.NewBash(), args) != Deny {
		t.Fatal("grant overrode deny")
	}
}

func TestCompoundCommandDoesNotInheritPrefixApproval(t *testing.T) {
	g := NewGateFromConfig("safe", Rules{Allow: []string{"Bash(git *)"}}, nil)
	if g.Decide(context.Background(), tools.NewBash(), map[string]any{"command": "git status; touch changed"}) != Ask {
		t.Fatal("compound command inherited git permission")
	}
	args := map[string]any{"command": "git status"}
	if got := ruleString("bash", args); got == "Bash(git *)" {
		t.Fatal("persistent approval broadened scope")
	}
}

func TestInvalidArgumentsNeverAllowed(t *testing.T) {
	g := NewGate(AlwaysProceed)
	if g.Decide(context.Background(), tools.NewBash(), map[string]any{"command": 42}) != Deny {
		t.Fatal("invalid arguments allowed")
	}
}

func TestOtherToolsHaveDistinctGrantKeys(t *testing.T) {
	if ruleKey("slurm_cancel", map[string]any{"job_id": "12"}) == ruleKey("slurm_cancel", map[string]any{"job_id": "13"}) {
		t.Fatal("different jobs share an approval")
	}
}
