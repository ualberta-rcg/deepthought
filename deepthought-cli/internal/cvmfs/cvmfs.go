// Package cvmfs provides typed software-discovery operations against the
// cluster's Lmod module tree. Site guidance (load patterns, the wheelhouse,
// troubleshooting) lives in the alliance-cvmfs skill; this package only runs
// the machine-readable `module spider` interface headlessly and parses its
// output. `module` is a shell function (not a binary), so every query goes
// through a login shell: `bash -lc "module …"`.
package cvmfs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Runner mirrors slurm.Runner so output parsing stays testable.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// ExecRunner runs a command and returns its combined output.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Client runs `module …` sub-commands headlessly.
type Client struct{ Runner Runner }

func NewClient(runner Runner) *Client {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Client{Runner: runner}
}

var (
	detectedOnce sync.Once
	detectedVal  bool
)

// Detected reports whether the Lmod module system is usable on this host. The
// Software screen and its F-key are gated on this, mirroring slurm.Detected.
// It first checks the cheap MODULESHOME env var, then falls back to probing a
// login shell for the module function; the result is cached for the process
// lifetime.
func Detected() bool {
	detectedOnce.Do(func() {
		if os.Getenv("MODULESHOME") != "" {
			detectedVal = true
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := ExecRunner{}.Run(ctx, "bash", "-lc", "type module >/dev/null 2>&1")
		detectedVal = err == nil
	})
	return detectedVal
}

// safeName is what a module name or name/version may contain. Anything else is
// rejected before we interpolate it into a login-shell command line.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+/ -]*$`)

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("cvmfs: empty module name")
	}
	if len(name) > 200 {
		return fmt.Errorf("cvmfs: module name too long")
	}
	if !safeName.MatchString(name) {
		return fmt.Errorf("cvmfs: unsafe characters in module name %q", name)
	}
	return nil
}

// SpiderResult is what `module spider <name>` reports for a single module.
type SpiderResult struct {
	Name        string
	Found       bool
	Versions    []string // "cuda/11.8", …
	Matches     []string // "Other possible module matches"
	Description string
	section     string // parse-only: the current output section
}

// SpiderDetail is what `module spider <name>/<ver>` reports: how to load that
// exact version.
type SpiderDetail struct {
	Full      string
	Found     bool
	LoadLines [][]string // each line = one valid set of prerequisites (any line works)
	LoadCmd   string     // recommended copyable command: "module load <deps> <full>"
}

// Spider returns the available versions (and related matches) for a module
// name. A name with no match is a normal outcome (Found=false, err=nil), not
// an error.
func (c *Client) Spider(ctx context.Context, name string) (SpiderResult, error) {
	if err := validateName(name); err != nil {
		return SpiderResult{}, err
	}
	raw, err := c.Runner.Run(ctx, "bash", "-lc", "module spider "+name)
	r := ParseSpider(raw)
	if err != nil && !isNotFound(err, raw) {
		return r, err
	}
	return r, nil
}

// SpiderDetail returns the load prerequisites for a specific name/version.
func (c *Client) SpiderDetail(ctx context.Context, name, ver string) (SpiderDetail, error) {
	full := name
	if ver != "" {
		full = name + "/" + ver
	}
	if err := validateName(full); err != nil {
		return SpiderDetail{}, err
	}
	raw, err := c.Runner.Run(ctx, "bash", "-lc", "module spider "+full)
	d := ParseSpiderDetail(full, raw)
	if err != nil && !d.Found && !isNotFound(err, raw) {
		return d, err
	}
	return d, nil
}

func isNotFound(err error, raw []byte) bool {
	var e string
	if err != nil {
		e = err.Error()
	}
	s := strings.ToLower(e + " " + string(raw))
	return strings.Contains(s, "unable to find") || strings.Contains(s, "no match")
}

// --- parsing ------------------------------------------------------------
// Parsers are pure (raw output in, struct out) so they are table-testable
// against captured Lmod output.

var (
	headerName = regexp.MustCompile(`^([A-Za-z0-9._+-]+):`)
)

// ParseSpider extracts the module name, its versions, and the "other possible
// module matches" from `module spider <name>` output.
func ParseSpider(raw []byte) (r SpiderResult) {
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "---") {
			r.section = "" // dashed rule: drop any in-progress section
			continue
		}
		if i := strings.Index(trimmed, "Other possible modules matches:"); i >= 0 {
			if after := strings.TrimSpace(trimmed[i+len("Other possible modules matches:"):]); after != "" {
				addMatches(&r, after)
			}
			r.section = "matches"
			continue
		}
		if strings.Contains(trimmed, "Versions:") {
			r.section = "versions"
			continue
		}
		if trimmed == "Description:" {
			r.section = "desc"
			continue
		}
		switch r.section {
		case "versions":
			if fields := strings.Fields(trimmed); len(fields) == 1 && strings.Contains(fields[0], "/") {
				r.Versions = append(r.Versions, fields[0])
				r.Found = true
			}
		case "desc":
			if trimmed != "" {
				r.Description += trimmed + "\n"
			}
		case "matches":
			addMatches(&r, trimmed)
		case "":
			if m := headerName.FindStringSubmatch(trimmed); m != nil && r.Name == "" {
				r.Name = m[1]
			}
		}
	}
	if r.Name == "" && len(r.Versions) > 0 {
		if i := strings.IndexByte(r.Versions[0], '/'); i > 0 {
			r.Name = r.Versions[0][:i]
		}
	}
	return r
}

func addMatches(r *SpiderResult, s string) {
	for _, tok := range strings.Fields(s) {
		tok = strings.TrimSuffix(tok, ",")
		if tok == "..." || !safeName.MatchString(tok) {
			continue
		}
		r.Matches = append(r.Matches, tok)
	}
}

// ParseSpiderDetail extracts the load prerequisites for `module spider
// <name>/<ver>` output. It handles the "You will need to load" block (multiple
// alternative dependency lines), the "can be loaded directly" case (no deps),
// and the "Unable to find" case (Found stays false).
func ParseSpiderDetail(full string, raw []byte) (d SpiderDetail) {
	d.Full = full
	text := string(raw)
	if isNotFound(nil, []byte(text)) {
		return d
	}
	// No-dependency modules: "This module can be loaded directly: module load X".
	if i := strings.Index(text, "can be loaded directly:"); i >= 0 {
		d.Found = true
		tail := strings.TrimSpace(text[i+len("can be loaded directly:"):])
		if j := strings.IndexByte(tail, '\n'); j >= 0 {
			tail = tail[:j]
		}
		if strings.HasPrefix(tail, "module load") {
			d.LoadCmd = strings.TrimSpace(tail)
		}
		return d
	}
	// Dependency block: collect name/version lines after the marker, up to the
	// blank line that follows the last one (or the Help: section).
	inLoad, seen := false, false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "You will need to load") {
			inLoad, d.Found = true, true
			continue
		}
		if !inLoad {
			continue
		}
		if strings.HasPrefix(trimmed, "Help:") {
			break
		}
		if trimmed == "" {
			if seen {
				break
			}
			continue // blank line before the load lines
		}
		fields := strings.Fields(trimmed)
		lineMods := make([]string, 0, len(fields))
		for _, f := range fields {
			if strings.Contains(f, "/") {
				lineMods = append(lineMods, f)
			}
		}
		if len(lineMods) > 0 {
			d.LoadLines = append(d.LoadLines, lineMods)
			seen = true
		}
	}
	if len(d.LoadLines) > 0 {
		d.Found = true
		d.LoadCmd = "module load " + strings.Join(append(append([]string{}, d.LoadLines[0]...), full), " ")
	}
	if d.LoadCmd == "" {
		d.LoadCmd = "module load " + full
	}
	return d
}
