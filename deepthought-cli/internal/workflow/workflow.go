// Package workflow persists approved scientific plans, evidence and attempts.
// It never launches a job: each submission goes through slurm_submit and Queen.
package workflow

import (
	"context"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tools"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Plan struct {
	Revision      int                          `json:"revision"`
	Directive     *history.Directive           `json:"directive"`
	Parameters    map[string]any               `json:"parameters"`
	ParameterHash string                       `json:"parameter_hash"`
	Artifacts     map[string]*history.Artifact `json:"artifacts"`
	Submissions   map[string]string            `json:"submissions"`
}
type Step struct {
	Description string            `json:"description"`
	Predicate   history.Predicate `json:"predicate"`
}
type Service struct {
	Store     *history.SQLiteStore
	Scheduler *slurm.Client
	Version   string
}

func (s *Service) List() ([]Plan, error) {
	rows, err := s.Store.Records("workflow")
	if err != nil {
		return nil, err
	}
	var out []Plan
	for _, r := range rows {
		var p Plan
		if err := json.Unmarshal(r.Data, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *Service) Get(id string) (*Plan, error) {
	var p Plan
	err := s.Store.GetRecord("workflow", id, &p)
	return &p, err
}
func (s *Service) Create(title string, params map[string]any, steps []Step) (*Plan, error) {
	if strings.TrimSpace(title) == "" || len(steps) == 0 || len(steps) > 32 {
		return nil, fmt.Errorf("a title and 1–32 sequential objectives are required")
	}
	d := history.NewDirective("local", title)
	var prior *history.Objective
	for _, step := range steps {
		if strings.TrimSpace(step.Description) == "" {
			return nil, fmt.Errorf("objective description is required")
		}
		o := d.AddObjective(step.Description, step.Predicate)
		o.PlanID = d.ID
		if prior != nil {
			o.Dependencies = []string{prior.ID}
			o.LinkTo(prior, "depends_on")
		} else {
			o.Status = history.StatusReady
		}
		prior = o
	}
	p := &Plan{Revision: 1, Directive: d, Parameters: params, ParameterHash: history.HashParameters(params), Artifacts: map[string]*history.Artifact{}, Submissions: map[string]string{}}
	if p.ParameterHash == "" {
		return nil, fmt.Errorf("parameters must be JSON values")
	}
	_, err := s.Store.ClaimRecord("workflow", d.ID, p)
	return p, err
}
func (s *Service) save(p *Plan) error {
	previous := p.Revision
	p.Revision++
	return s.Store.ReplaceRecord("workflow", p.Directive.ID, previous, p)
}
func objective(p *Plan, id string) (*history.Objective, error) {
	for _, o := range p.Directive.Objectives {
		if o.ID == id {
			return o, nil
		}
	}
	return nil, fmt.Errorf("unknown objective")
}
func frontier(p *Plan, o *history.Objective) error {
	for _, id := range o.Dependencies {
		up, err := objective(p, id)
		if err != nil || up.Status != history.StatusSucceeded {
			return fmt.Errorf("validate predecessor objectives first")
		}
	}
	return nil
}
func (s *Service) Link(id, objectiveID, submissionID string) (*Plan, error) {
	p, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	o, err := objective(p, objectiveID)
	if err != nil {
		return nil, err
	}
	if err := frontier(p, o); err != nil {
		return nil, err
	}
	var sub slurm.Submission
	if err := s.Store.GetRecord("submission", submissionID, &sub); err != nil {
		return nil, err
	}
	if sub.JobID == "" {
		return nil, fmt.Errorf("reconcile unresolved submission before linking")
	}
	if p.Submissions[o.ID] == submissionID {
		return p, nil
	}
	attempt := o.StartAttempt(s.Version)
	attempt.Environment.JobID = sub.JobID
	attempt.Metadata = map[string]any{"submission_id": submissionID, "script_hash": sub.ScriptHash, "parameter_hash": p.ParameterHash}
	p.Submissions[o.ID] = submissionID
	p.Directive.Status = history.StatusRunning
	return p, s.save(p)
}
func (s *Service) Validate(ctx context.Context, id, objectiveID, artifactPath, tier string) (*Plan, error) {
	p, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	o, err := objective(p, objectiveID)
	if err != nil {
		return nil, err
	}
	if err := frontier(p, o); err != nil {
		return nil, err
	}
	predicate := o.Predicate
	if predicate.Kind == history.PredicateExitZero {
		if s.Scheduler == nil {
			return nil, fmt.Errorf("scheduler unavailable")
		}
		if _, err := s.Scheduler.Reconcile(ctx); err != nil {
			return nil, err
		}
		var sub slurm.Submission
		if err := s.Store.GetRecord("submission", p.Submissions[o.ID], &sub); err != nil {
			return nil, fmt.Errorf("link a submitted attempt before validating exit status")
		}
		if strings.ToUpper(sub.State) != "COMPLETED" {
			return nil, fmt.Errorf("job is not confirmed completed: %s", sub.State)
		}
		predicate.ExitCode = 0
	} else if predicate.Path != "" {
		real, err := tools.OwnedPath(predicate.Path)
		if err != nil {
			return nil, err
		}
		predicate.Path = real
		if predicate.Kind == history.PredicateNumeric {
			info, err := os.Stat(real)
			if err != nil {
				return nil, err
			}
			if info.Size() > 4096 {
				return nil, fmt.Errorf("numeric result exceeds 4 KiB")
			}
			raw, err := os.ReadFile(real)
			if err != nil {
				return nil, err
			}
			predicate.Value, err = strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
			if err != nil {
				return nil, err
			}
		}
	} else if predicate.Kind == history.PredicateNumeric {
		return nil, fmt.Errorf("numeric validation requires a result file")
	}
	passed, err := predicate.Evaluate(ctx)
	if err != nil {
		return nil, err
	}
	o.Status = history.StatusFailed
	if passed {
		o.Status = history.StatusSucceeded
	}
	if passed && artifactPath != "" {
		real, err := tools.OwnedPath(artifactPath)
		if err != nil {
			return nil, err
		}
		consumed := map[string]string{"parameters": p.ParameterHash}
		for _, dep := range o.Dependencies {
			up, _ := objective(p, dep)
			for _, key := range up.Outputs {
				if a := p.Artifacts[key]; a != nil {
					consumed[key] = a.Hash
				}
			}
		}
		a, err := history.NewArtifact("local", real, tier, nil, consumed)
		if err != nil {
			return nil, err
		}
		a.PlanID = p.Directive.ID
		a.LinkTo(o, "produced_by")
		p.Artifacts[a.ID] = a
		o.Outputs = append(o.Outputs, a.ID)
	}
	if len(o.Attempts) > 0 {
		attempt := o.Attempts[len(o.Attempts)-1]
		if passed {
			attempt.MarkCompleted()
		} else {
			attempt.Fail("objective predicate failed")
		}
	}
	all := true
	for _, step := range p.Directive.Objectives {
		if step.Status != history.StatusSucceeded {
			all = false
		}
		if step.Status == history.StatusPending && frontier(p, step) == nil {
			step.Status = history.StatusReady
		}
	}
	if all {
		p.Directive.Status = history.StatusSucceeded
	} else if !passed {
		p.Directive.Status = history.StatusFailed
	}
	return p, s.save(p)
}
func (s *Service) Reparameter(id string, params map[string]any) (*Plan, error) {
	p, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	hash := history.HashParameters(params)
	if hash == "" {
		return nil, fmt.Errorf("parameters must be JSON values")
	}
	if hash == p.ParameterHash {
		return p, nil
	}
	p.Parameters, p.ParameterHash = params, hash
	history.StaleArtifacts(p.Artifacts, map[string]string{"parameters": hash})
	p.Directive.Version++
	p.Directive.Status = history.StatusPending
	for i, o := range p.Directive.Objectives {
		o.Outputs = nil
		o.Status = history.StatusPending
		if i == 0 {
			o.Status = history.StatusReady
		}
	}
	p.Submissions = map[string]string{}
	return p, s.save(p)
}
