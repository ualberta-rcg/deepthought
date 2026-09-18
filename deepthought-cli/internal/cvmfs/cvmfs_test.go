package cvmfs

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// Fixtures are trimmed captures of real Lmod `module spider` output from the
// cluster login node (Modules based on Lua, Lmod 8.7).

const cudaSpider = `
----------------------------------------------------------------------------
  cuda:
----------------------------------------------------------------------------
    Description:
      CUDA (formerly Compute Unified Device Architecture) is a parallel
      computing platform and programming model created by NVIDIA.

     Versions:
        cuda/11.8
        cuda/12.2
        cuda/12.6
        cuda/12.9
        cuda/13.2
     Other possible modules matches:
        chapel-ucx-cuda  cudacompat  cudacore  fosscuda  gcccorecuda  ...

----------------------------------------------------------------------------
  To find other possible module matches execute:

      $ module -r spider '.*cuda.*'
----------------------------------------------------------------------------
`

const cudaDetail = `
----------------------------------------------------------------------------
  cuda: cuda/13.2
----------------------------------------------------------------------------
    Description:
      CUDA (formerly Compute Unified Device Architecture).

    Properties:
      Tools for development

    You will need to load all module(s) on any one of the lines below before the "cuda/13.2" module is available to load.

      StdEnv/2023  gcc/12.3
      StdEnv/2023  gcc/12.3  openmpi/4.1.5

    Help:
      Description
      ===========
      CUDA ...
`

const stdenvDetail = `
----------------------------------------------------------------------------
  StdEnv: StdEnv/2023
----------------------------------------------------------------------------
    Properties:
      Module is Sticky, requires --force to unload or purge

    This module can be loaded directly: module load StdEnv/2023
`

const notFound = `Lmod has detected the following error: Unable to find: "zzznotamodule123".`

type fakeRunner struct {
	out []byte
	err error
}

func (f fakeRunner) Run(context.Context, string, ...string) ([]byte, error) { return f.out, f.err }

func TestParseSpiderVersions(t *testing.T) {
	r := ParseSpider([]byte(cudaSpider))
	if !r.Found {
		t.Fatalf("expected Found")
	}
	if r.Name != "cuda" {
		t.Errorf("Name = %q, want cuda", r.Name)
	}
	wantVersions := []string{"cuda/11.8", "cuda/12.2", "cuda/12.6", "cuda/12.9", "cuda/13.2"}
	if !reflect.DeepEqual(r.Versions, wantVersions) {
		t.Errorf("Versions = %v, want %v", r.Versions, wantVersions)
	}
	// The trailing "..." is filtered; the five matches are on the line after
	// the "Other possible modules matches:" header.
	wantMatches := []string{"chapel-ucx-cuda", "cudacompat", "cudacore", "fosscuda", "gcccorecuda"}
	if !reflect.DeepEqual(r.Matches, wantMatches) {
		t.Errorf("Matches = %v, want %v", r.Matches, wantMatches)
	}
}

func TestParseSpiderDetailDeps(t *testing.T) {
	d := ParseSpiderDetail("cuda/13.2", []byte(cudaDetail))
	if !d.Found {
		t.Fatalf("expected Found")
	}
	wantLines := [][]string{
		{"StdEnv/2023", "gcc/12.3"},
		{"StdEnv/2023", "gcc/12.3", "openmpi/4.1.5"},
	}
	if !reflect.DeepEqual(d.LoadLines, wantLines) {
		t.Errorf("LoadLines = %v, want %v", d.LoadLines, wantLines)
	}
	// The recommended command uses the first (fewest-dep) line + the module.
	if d.LoadCmd != "module load StdEnv/2023 gcc/12.3 cuda/13.2" {
		t.Errorf("LoadCmd = %q", d.LoadCmd)
	}
}

func TestParseSpiderDetailNoDeps(t *testing.T) {
	d := ParseSpiderDetail("StdEnv/2023", []byte(stdenvDetail))
	if !d.Found {
		t.Fatalf("expected Found")
	}
	if len(d.LoadLines) != 0 {
		t.Errorf("no-dep module should have no LoadLines: %v", d.LoadLines)
	}
	if d.LoadCmd != "module load StdEnv/2023" {
		t.Errorf("LoadCmd = %q, want %q", d.LoadCmd, "module load StdEnv/2023")
	}
}

func TestParseSpiderDetailNotFound(t *testing.T) {
	d := ParseSpiderDetail("zzznotamodule123", []byte(notFound))
	if d.Found {
		t.Errorf("expected Found=false for a missing module")
	}
}

func TestSpiderMethod(t *testing.T) {
	ctx := context.Background()
	// Found: no error, versions populated.
	c := NewClient(fakeRunner{out: []byte(cudaSpider)})
	r, err := c.Spider(ctx, "cuda")
	if err != nil {
		t.Fatalf("Spider(err): %v", err)
	}
	if !r.Found || len(r.Versions) != 5 {
		t.Errorf("Spider = %+v, err=%v", r, err)
	}

	// Not found is a normal outcome (no error), not an error.
	c = NewClient(fakeRunner{out: []byte(notFound), err: errors.New("bash: 1: Unable to find: zzz")})
	r, err = c.Spider(ctx, "zzznotamodule123")
	if err != nil {
		t.Fatalf("not-found should not be an error: %v", err)
	}
	if r.Found {
		t.Errorf("expected Found=false")
	}
}

func TestSpiderValidatesName(t *testing.T) {
	c := NewClient(fakeRunner{out: []byte(cudaSpider)})
	// Shell metacharacters and empties must be rejected before any subprocess.
	for _, bad := range []string{"", "cuda; rm -rf /", "cuda & whoami", "$(reboot)"} {
		if _, err := c.Spider(context.Background(), bad); err == nil {
			t.Errorf("Spider(%q): expected validation error, got nil", bad)
		}
	}
	// A plain module name must pass validation and reach the runner.
	if _, err := c.Spider(context.Background(), "cuda/13.2"); err != nil {
		t.Errorf("Spider(cuda/13.2): unexpected error %v", err)
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"cuda", true},
		{"cuda/13.2", true},
		{"gcccorecuda-1.2", true},
		{"", false},
		{"cuda; rm", false},
		{"cuda && x", false},
		{"$(x)", false},
		{"cu'da", false},
	}
	for _, tc := range cases {
		err := validateName(tc.name)
		if (err == nil) != tc.ok {
			t.Errorf("validateName(%q) err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}
