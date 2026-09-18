package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"

	"deepthought-cli/internal/cvmfs"
)

func softwareModel(detected bool) SoftwareModel {
	ti := textinput.New()
	ti.Prompt = "⌕ "
	ti.Placeholder = "search modules… (e.g. cuda, python, openmpi)"
	ti.Focus() // the placeholder only renders while the box is focused
	return SoftwareModel{
		input:    ti,
		client:   cvmfs.NewClient(nil),
		detected: detected,
		phase:    phaseSearch,
		vp:       viewport.New(),
	}
}

func TestSoftwareScreenReferenceBlocks(t *testing.T) {
	v := softwareModel(true).Resize(100, 40).View()
	for _, want := range []string{
		"Common stacks", "CVMFS roots", "Notes",
		"/cvmfs/soft.computecanada.ca", "StdEnv/2023",
		"module avail is tier-incomplete", "⌕",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("software view missing %q", want)
		}
	}
}

func TestSoftwareScreenNoModules(t *testing.T) {
	v := softwareModel(false).Resize(100, 30).View()
	if !strings.Contains(v, "Modules (Lmod) not detected") {
		t.Errorf("expected 'Modules (Lmod) not detected': %q", v)
	}
}

func TestSoftwareScreenResults(t *testing.T) {
	m := softwareModel(true)
	m.phase = phaseResults
	m.query = "cuda"
	m.result = cvmfs.SpiderResult{Name: "cuda", Found: true, Versions: []string{"cuda/11.8", "cuda/13.2"}}
	v := m.Resize(100, 30).View()
	for _, want := range []string{"2 versions", "cuda/11.8", "cuda/13.2", "▶"} {
		if !strings.Contains(v, want) {
			t.Errorf("results view missing %q", want)
		}
	}
}

func TestSoftwareScreenDetail(t *testing.T) {
	m := softwareModel(true)
	m.phase = phaseDetail
	m.detail = cvmfs.SpiderDetail{
		Full:      "cuda/13.2",
		Found:     true,
		LoadLines: [][]string{{"StdEnv/2023", "gcc/12.3"}},
		LoadCmd:   "module load StdEnv/2023 gcc/12.3 cuda/13.2",
	}
	v := m.Resize(100, 30).View()
	for _, want := range []string{"How to load cuda/13.2", "StdEnv/2023  gcc/12.3", "module load StdEnv/2023 gcc/12.3 cuda/13.2"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail view missing %q", want)
		}
	}
}

// Regression: the search box must be focused on construction, or textinput
// drops every keystroke (its Update returns early when !Focused). This was the
// "can't type" bug.
func TestSoftwareInputFocused(t *testing.T) {
	m := NewSoftwareModel().Resize(100, 40)
	if !m.input.Focused() {
		t.Error("search input should be focused on construction (else it drops keystrokes)")
	}
}

func TestSoftwareScreenNotFound(t *testing.T) {
	m := softwareModel(true)
	m.phase = phaseSearch
	m.haveResult = true
	m.query = "zzz"
	m.result = cvmfs.SpiderResult{Found: false, Matches: []string{"zzztier", "foo"}}
	v := m.Resize(100, 30).View()
	for _, want := range []string{`no module matches "zzz"`, "did you mean", "zzztier"} {
		if !strings.Contains(v, want) {
			t.Errorf("not-found view missing %q", want)
		}
	}
}
