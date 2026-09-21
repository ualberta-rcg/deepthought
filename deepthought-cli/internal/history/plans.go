package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type DomainStatus string

const (
	StatusPending    DomainStatus = "pending"
	StatusReady      DomainStatus = "ready"
	StatusRunning    DomainStatus = "running"
	StatusSucceeded  DomainStatus = "succeeded"
	StatusFailed     DomainStatus = "failed"
	StatusUnverified DomainStatus = "unverified"
)

type PredicateKind string

const (
	PredicateExitZero   PredicateKind = "exit_zero"
	PredicateFileExists PredicateKind = "file_exists"
	PredicateHashMatch  PredicateKind = "hash_matches"
	PredicateRegexIn    PredicateKind = "regex_in"
	PredicateNumeric    PredicateKind = "numeric_bound"
	PredicateModelJudge PredicateKind = "model_judge"
)

type Predicate struct {
	Kind     PredicateKind `json:"kind"`
	Path     string        `json:"path,omitempty"`
	Expected string        `json:"expected,omitempty"`
	Pattern  string        `json:"pattern,omitempty"`
	Value    float64       `json:"value,omitempty"`
	Min      *float64      `json:"min,omitempty"`
	Max      *float64      `json:"max,omitempty"`
	ExitCode int           `json:"exit_code,omitempty"`
}

func (p Predicate) Evaluate(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch p.Kind {
	case PredicateExitZero:
		return p.ExitCode == 0, nil
	case PredicateFileExists:
		_, err := os.Stat(p.Path)
		return err == nil, nil
	case PredicateHashMatch:
		hash, err := hashArtifact(ctx, p.Path)
		return hash == strings.ToLower(p.Expected) && err == nil, err
	case PredicateRegexIn:
		f, err := os.Open(p.Path)
		if err != nil {
			return false, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return false, fmt.Errorf("regex predicate needs a regular file of at most 1 MiB")
		}
		raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		if err != nil {
			return false, err
		}
		expression, err := regexp.Compile(p.Pattern)
		return err == nil && expression.Match(raw), err
	case PredicateNumeric:
		if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) || (p.Min != nil && (math.IsNaN(*p.Min) || math.IsInf(*p.Min, 0))) || (p.Max != nil && (math.IsNaN(*p.Max) || math.IsInf(*p.Max, 0))) {
			return false, fmt.Errorf("numeric predicate requires finite values")
		}
		if p.Min != nil && p.Value < *p.Min {
			return false, nil
		}
		if p.Max != nil && p.Value > *p.Max {
			return false, nil
		}
		return true, nil
	case PredicateModelJudge:
		return false, fmt.Errorf("model_judge requires explicit sign-off and remains unverified")
	default:
		return false, fmt.Errorf("unknown predicate %q", p.Kind)
	}
}

type Budget struct {
	Tokens, WallclockSeconds int
	USD, GPUHours            float64
}

type Constraints struct {
	MaxSensitivity   Sensitivity
	ApprovalRequired bool
	Partition        string
	StorageTier      string
}

type Directive struct {
	Vinculum
	State      State
	Status     DomainStatus
	Version    int
	Title      string
	Objectives []*Objective
	Diff       string
}

type Objective struct {
	Vinculum
	State        State
	Status       DomainStatus
	Description  string
	Inputs       []string
	Outputs      []string
	Dependencies []string
	Predicate    Predicate
	Budget       Budget
	Constraints  Constraints
	Attempts     []*Incursion
	Children     []*Objective
}

type Artifact struct {
	Vinculum
	State          State
	Status         DomainStatus
	Path           string
	Hash           string
	StorageTier    string
	PurgeExpiry    *time.Time
	ConsumedHashes map[string]string
	Stale          bool
}

type Environment struct {
	Modules         []string          `json:"modules,omitempty"`
	ContainerDigest string            `json:"container_digest,omitempty"`
	Hardware        string            `json:"hardware,omitempty"`
	Driver          string            `json:"driver,omitempty"`
	Toolkit         string            `json:"toolkit,omitempty"`
	Seeds           map[string]string `json:"seeds,omitempty"`
	JobID           string            `json:"job_id,omitempty"`
	NodeList        string            `json:"node_list,omitempty"`
	HarnessVersion  string            `json:"harness_version,omitempty"`
	CapturedAt      time.Time         `json:"captured_at"`
}

func CaptureEnvironment(harnessVersion string) Environment {
	env := Environment{
		Hardware: runtime.GOOS + "/" + runtime.GOARCH, Driver: os.Getenv("NVIDIA_DRIVER_VERSION"),
		Toolkit: os.Getenv("CUDA_VERSION"), ContainerDigest: os.Getenv("APPTAINER_CONTAINER"),
		JobID: os.Getenv("SLURM_JOB_ID"), NodeList: os.Getenv("SLURM_JOB_NODELIST"),
		HarnessVersion: harnessVersion, CapturedAt: time.Now(), Seeds: map[string]string{},
	}
	if modules := os.Getenv("LOADEDMODULES"); modules != "" {
		env.Modules = strings.Split(modules, ":")
	}
	for _, key := range []string{"SEED", "PYTHONHASHSEED", "TORCH_SEED"} {
		if value := os.Getenv(key); value != "" {
			env.Seeds[key] = value
		}
	}
	return env
}

func NewDirective(sessionID, title string) *Directive {
	return &Directive{Vinculum: Vinculum{
		ID: newUUIDv7(), Kind: "directive", SessionID: sessionID, CreatedAt: time.Now(),
	}, State: StateFull, Status: StatusPending, Version: 1, Title: title}
}

func (d *Directive) AddObjective(description string, predicate Predicate) *Objective {
	objective := &Objective{Vinculum: Vinculum{
		ID: newUUIDv7(), Kind: "objective", SessionID: d.SessionID,
		ParentID: d.ID, Index: len(d.Objectives), CreatedAt: time.Now(),
	}, State: StateFull, Status: StatusPending, Description: description, Predicate: predicate}
	objective.LinkTo(d, "belongs_to")
	d.Objectives = append(d.Objectives, objective)
	return objective
}

// Decompose lazily creates children only when an objective reaches the frontier.
func (o *Objective) Decompose(descriptions ...string) ([]*Objective, error) {
	if o.Status != StatusReady && o.Status != StatusRunning {
		return nil, fmt.Errorf("objective %s is not at the execution frontier", o.ID)
	}
	if len(o.Children) > 0 {
		return o.Children, nil
	}
	for _, description := range descriptions {
		child := &Objective{Vinculum: Vinculum{
			ID: newUUIDv7(), Kind: "objective", SessionID: o.SessionID,
			ParentID: o.ID, Index: len(o.Children), CreatedAt: time.Now(),
		}, State: StateFull, Status: StatusPending, Description: description}
		child.LinkTo(o, "breaks_into")
		o.Children = append(o.Children, child)
	}
	return o.Children, nil
}

func (o *Objective) StartAttempt(harnessVersion string) *Incursion {
	attempt := &Incursion{
		Vinculum: Vinculum{ID: newUUIDv7(), Kind: "incursion", SessionID: o.SessionID,
			ParentID: o.ID, Index: len(o.Attempts), CreatedAt: time.Now()},
		Status: IncursionStreaming, ObjectiveID: o.ID,
		AttemptNumber: len(o.Attempts) + 1, Environment: CaptureEnvironment(harnessVersion),
	}
	attempt.LinkTo(o, "attempt_of")
	o.Attempts = append(o.Attempts, attempt)
	o.Status = StatusRunning
	return attempt
}

func (d *Directive) Supersede(diff string) *Directive {
	next := NewDirective(d.SessionID, d.Title)
	next.Version = d.Version + 1
	next.Diff = diff
	raw, _ := json.Marshal(d.Objectives)
	_ = json.Unmarshal(raw, &next.Objectives)
	for _, o := range next.Objectives {
		o.ParentID = next.ID
		o.PlanID = next.ID
	}
	next.LinkTo(d, "supersedes")
	return next
}

func NewArtifact(sessionID, path, tier string, purge *time.Time, consumed map[string]string) (*Artifact, error) {
	hash, err := hashArtifact(context.Background(), filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	return &Artifact{
		Vinculum: Vinculum{ID: newUUIDv7(), Kind: "artifact", SessionID: sessionID, CreatedAt: time.Now()},
		State:    StateFull, Status: StatusSucceeded, Path: path, Hash: hash,
		StorageTier: tier, PurgeExpiry: purge, ConsumedHashes: consumed,
	}, nil
}

// StaleArtifacts computes transitive invalidation against current input hashes.
func StaleArtifacts(artifacts map[string]*Artifact, currentHashes map[string]string) []string {
	stale := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for id, artifact := range artifacts {
			if stale[id] {
				continue
			}
			for input, recorded := range artifact.ConsumedHashes {
				current := currentHashes[input]
				if upstream := artifacts[input]; upstream != nil {
					current = upstream.Hash
				}
				if current != recorded || stale[input] {
					stale[id], artifact.Stale, changed = true, true, true
					break
				}
			}
		}
	}
	out := make([]string, 0, len(stale))
	for id := range stale {
		out = append(out, id)
	}
	return out
}

func SpendApproval(sessionID, objectiveID, summary string, allowed bool) (*Drone, error) {
	drone, err := NewDrone("approval", sessionID, map[string]any{
		"objective_id": objectiveID, "summary": summary, "allowed": allowed,
	})
	if err != nil {
		return nil, err
	}
	drone.Links = append(drone.Links, Link{
		SourceID: drone.ID, TargetID: objectiveID, Relation: "approves",
		Kind: "objective", CreatedAt: time.Now(),
	})
	return drone, nil
}

func HashParameters(values map[string]any) string {
	raw, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func ParseNumeric(value string) (float64, error) { return strconv.ParseFloat(value, 64) }

func hashArtifact(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return "", fmt.Errorf("hash validation is bounded to 64 MiB; hash larger artifacts inside a compute job")
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		total += n
		if total > 64<<20 {
			return "", fmt.Errorf("artifact grew beyond 64 MiB")
		}
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
