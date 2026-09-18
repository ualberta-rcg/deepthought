package skills

import (
	"strings"
	"testing"
)

func TestListing(t *testing.T) {
	if got := Listing(nil); got != "" {
		t.Fatalf("Listing(nil) = %q, want empty", got)
	}
	sk := []*Skill{
		{Frontmatter: Frontmatter{Name: "alliance-slurm", Description: "Jobs and GPUs", WhenToUse: "before submitting a job\n  or touching a GPU"}},
		{Frontmatter: Frontmatter{Name: "alliance-cvmfs", Description: "Software and modules"}},
	}
	got := Listing(sk)
	for _, want := range []string{
		"Available skills",
		"alliance-slurm: Jobs and GPUs",
		"alliance-cvmfs: Software and modules",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Listing missing %q:\n%s", want, got)
		}
	}
	// when_to_use is collapsed to one line.
	if !strings.Contains(got, "use when: before submitting a job or touching a GPU") {
		t.Errorf("when-to-use not collapsed to one line:\n%s", got)
	}
}
