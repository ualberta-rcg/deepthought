package queen

import (
	"context"
	"testing"

	"deepthought-cli/internal/tools"
)

func TestReadOnlyAsksInSafe(t *testing.T) {
	g := NewGate(Review) // Review → OpSafe: read asks
	if d := g.Decide(context.Background(), tools.NewRead(), map[string]any{"file_path": "/tmp/x"}); d != Ask {
		t.Errorf("read in Safe = %v, want Ask", d)
	}
}

func TestReadOnlyAllowedInSafeAuto(t *testing.T) {
	g := NewGateFromConfig("safe-auto", Rules{}, nil)
	if d := g.Decide(context.Background(), tools.NewRead(), map[string]any{"file_path": "/tmp/x"}); d != Allow {
		t.Errorf("read in SafeAuto = %v, want Allow", d)
	}
}

func TestBashAsksInReview(t *testing.T) {
	g := NewGate(Review)
	d := g.Decide(context.Background(), tools.NewBash(), map[string]any{"command": "ls"})
	if d != Ask {
		t.Errorf("bash(ls) in Review = %v, want Ask", d)
	}
}

func TestBashAllowedInAlwaysProceed(t *testing.T) {
	g := NewGate(AlwaysProceed)
	d := g.Decide(context.Background(), tools.NewBash(), map[string]any{"command": "ls"})
	if d != Allow {
		t.Errorf("bash(ls) in AlwaysProceed = %v, want Allow", d)
	}
}

func TestDestructiveDeniedEvenInAlwaysProceed(t *testing.T) {
	g := NewGate(AlwaysProceed)
	cases := []string{
		"rm -rf /",
		"rm -rf ~",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda bs=1M",
		":(){ :|:& };:",
		"shutdown -h now",
	}
	for _, cmd := range cases {
		d := g.Decide(context.Background(), tools.NewBash(), map[string]any{"command": cmd})
		if d != Deny {
			t.Errorf("destructive %q in AlwaysProceed = %v, want Deny", cmd, d)
		}
	}
}

func TestNonDestructiveRmAllowed(t *testing.T) {
	// `rm` of a specific file is NOT caught by the denylist (only recursive-of-roots).
	g := NewGate(Review)
	d := g.Decide(context.Background(), tools.NewBash(), map[string]any{"command": "rm /tmp/some-file"})
	if d != Ask {
		t.Errorf("rm of a file = %v, want Ask (not destructive, but mutating)", d)
	}
}

func TestDestructiveReason(t *testing.T) {
	if DestructiveReason("rm -rf /") == "" {
		t.Error("want a reason for rm -rf /")
	}
	if DestructiveReason("ls") != "" {
		t.Error("want no reason for ls")
	}
}

func TestGateCloneIsolatesTaskGrants(t *testing.T) {
	parent := NewGateFromConfig("safe-auto", Rules{Allow: []string{"Bash(git *)"}}, nil)
	parent.GrantTask("bash", map[string]any{"command": "ls"})
	child := parent.Clone()
	if child == nil {
		t.Fatal("Clone returned nil")
	}
	if child.OpModeName() != "safe-auto" {
		t.Fatalf("cloned mode = %q", child.OpModeName())
	}
	if len(child.Rules.Allow) != 1 || child.Rules.Allow[0] != "Bash(git *)" {
		t.Fatalf("cloned rules = %#v", child.Rules)
	}
	// Parent's task grant must not appear on the child.
	d := child.Decide(context.Background(), tools.NewBash(), map[string]any{"command": "ls"})
	if d != Ask {
		t.Errorf("cloned gate still holds parent task grant: got %v, want Ask", d)
	}
}
