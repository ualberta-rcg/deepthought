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
	"time"
)

// StepExemplar is a captured Worked Example from a validated scientific step.
type StepExemplar struct {
	ID             string         `json:"id"`
	PlanID         string         `json:"plan_id"`
	ObjectiveID    string         `json:"objective_id"`
	StepIndex      int            `json:"step_index"`
	Description    string         `json:"description"`
	SystemID       string         `json:"system_id,omitempty"`
	Cluster        string         `json:"cluster,omitempty"`
	SubmissionID   string         `json:"submission_id,omitempty"`
	JobID          string         `json:"job_id,omitempty"`
	ScriptPath     string         `json:"script_path,omitempty"`
	ScriptHash     string         `json:"script_hash,omitempty"`
	ArtifactPath   string         `json:"artifact_path,omitempty"`
	ArtifactHash   string         `json:"artifact_hash,omitempty"`
	Commands       []string       `json:"commands,omitempty"`
	Parameters     map[string]any `json:"parameters,omitempty"`
	VerifiedOutput string         `json:"verified_output,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

// PipelineTemplate is a reusable scientific pipeline with per-step Worked Examples.
type PipelineTemplate struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Description   string               `json:"description,omitempty"`
	SourcePlanID  string               `json:"source_plan_id,omitempty"`
	DefaultParams map[string]any       `json:"default_params"`
	Steps         []Step               `json:"steps"`
	Exemplars     map[int]StepExemplar `json:"exemplars,omitempty"`
	CreatedAt     string               `json:"created_at"`
	UpdatedAt     string               `json:"updated_at"`
}

type Plan struct {
	Revision      int                          `json:"revision"`
	TemplateID    string                       `json:"template_id,omitempty"`
	SystemID      string                       `json:"system_id,omitempty"`
	Cluster       string                       `json:"cluster,omitempty"`
	Directive     *history.Directive           `json:"directive"`
	Parameters    map[string]any               `json:"parameters"`
	ParameterHash string                       `json:"parameter_hash"`
	Artifacts     map[string]*history.Artifact `json:"artifacts"`
	Submissions   map[string]string            `json:"submissions"`
	Exemplars     map[string]StepExemplar      `json:"exemplars,omitempty"`
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
	if passed {
		s.captureStepExemplar(p, o, artifactPath)
	}
	return p, s.save(p)
}

func (s *Service) captureStepExemplar(p *Plan, o *history.Objective, artifactPath string) {
	if p == nil || o == nil {
		return
	}
	if p.Exemplars == nil {
		p.Exemplars = map[string]StepExemplar{}
	}
	stepIdx := 0
	for i, step := range p.Directive.Objectives {
		if step.ID == o.ID {
			stepIdx = i
			break
		}
	}
	ex := StepExemplar{
		ID:           fmt.Sprintf("ex_%s_%s", p.Directive.ID, o.ID),
		PlanID:       p.Directive.ID,
		ObjectiveID:  o.ID,
		StepIndex:    stepIdx,
		Description:  o.Description,
		SystemID:     p.SystemID,
		Cluster:      p.Cluster,
		SubmissionID: p.Submissions[o.ID],
		ArtifactPath: artifactPath,
		Parameters:   p.Parameters,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if subID := p.Submissions[o.ID]; subID != "" && s.Store != nil {
		var sub slurm.Submission
		if s.Store.GetRecord("submission", subID, &sub) == nil {
			ex.JobID = sub.JobID
			ex.ScriptPath = sub.ScriptPath
			ex.ScriptHash = sub.ScriptHash
			if ex.SystemID == "" {
				ex.SystemID = sub.SystemID
			}
			if ex.Cluster == "" {
				ex.Cluster = sub.Cluster
			}
		}
	}
	if len(o.Outputs) > 0 {
		lastArt := o.Outputs[len(o.Outputs)-1]
		if art := p.Artifacts[lastArt]; art != nil {
			ex.ArtifactHash = art.Hash
			if ex.ArtifactPath == "" {
				ex.ArtifactPath = art.Path
			}
		}
	}
	if s.Store != nil {
		if execs, err := s.Store.ListToolExecutions("", "", 12); err == nil {
			for _, rec := range execs {
				if rec.Command != "" && !rec.IsError {
					ex.Commands = append(ex.Commands, rec.ToolName+": "+rec.Command)
					if len(ex.Commands) >= 5 {
						break
					}
				}
			}
		}
	}
	ex.VerifiedOutput = fmt.Sprintf("predicate=%s status=%s", o.Predicate.Kind, o.Status)
	p.Exemplars[o.ID] = ex
	if s.Store != nil {
		_ = s.Store.PutRecord("step-exemplar", ex.ID, ex)
	}
}

// SaveTemplate snapshots a plan and its captured StepExemplars into a reusable PipelineTemplate.
func (s *Service) SaveTemplate(planID, name, description string) (*PipelineTemplate, error) {
	p, err := s.Get(planID)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = p.Directive.Title
	}
	steps := make([]Step, 0, len(p.Directive.Objectives))
	exemplars := map[int]StepExemplar{}
	for i, obj := range p.Directive.Objectives {
		steps = append(steps, Step{
			Description: obj.Description,
			Predicate:   obj.Predicate,
		})
		if ex, ok := p.Exemplars[obj.ID]; ok {
			ex.StepIndex = i
			exemplars[i] = ex
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tplID := "tpl_" + history.HashParameters(map[string]any{"name": name, "source": planID})[:12]
	tpl := &PipelineTemplate{
		ID:            tplID,
		Name:          name,
		Description:   strings.TrimSpace(description),
		SourcePlanID:  planID,
		DefaultParams: p.Parameters,
		Steps:         steps,
		Exemplars:     exemplars,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.Store.PutRecord("pipeline-template", tpl.ID, tpl); err != nil {
		return nil, err
	}
	return tpl, nil
}

// ListTemplates returns all saved PipelineTemplates.
func (s *Service) ListTemplates() ([]PipelineTemplate, error) {
	rows, err := s.Store.Records("pipeline-template")
	if err != nil {
		return nil, err
	}
	var out []PipelineTemplate
	for _, r := range rows {
		var tpl PipelineTemplate
		if err := json.Unmarshal(r.Data, &tpl); err != nil {
			continue
		}
		out = append(out, tpl)
	}
	return out, nil
}

// InstantiateTemplate creates a new Plan from a saved PipelineTemplate, carrying over its StepExemplars.
func (s *Service) InstantiateTemplate(templateID, title string, overrideParams map[string]any) (*Plan, error) {
	var tpl PipelineTemplate
	if err := s.Store.GetRecord("pipeline-template", strings.TrimSpace(templateID), &tpl); err != nil {
		return nil, fmt.Errorf("pipeline template %q not found: %w", templateID, err)
	}
	merged := map[string]any{}
	for k, v := range tpl.DefaultParams {
		merged[k] = v
	}
	for k, v := range overrideParams {
		merged[k] = v
	}
	if strings.TrimSpace(title) == "" {
		title = tpl.Name
	}
	plan, err := s.Create(title, merged, tpl.Steps)
	if err != nil {
		return nil, err
	}
	plan.TemplateID = tpl.ID
	plan.Exemplars = map[string]StepExemplar{}
	for i, obj := range plan.Directive.Objectives {
		if ex, ok := tpl.Exemplars[i]; ok {
			plan.Exemplars[obj.ID] = ex
		}
	}
	if err := s.save(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// StepContext formats the active objective + its Worked Example (StepExemplar) for model context injection.
func (s *Service) StepContext(planID, objectiveID string) (map[string]any, error) {
	p, err := s.Get(planID)
	if err != nil {
		return nil, err
	}
	var target *history.Objective
	stepIdx := 0
	for i, o := range p.Directive.Objectives {
		if (objectiveID != "" && o.ID == objectiveID) || (objectiveID == "" && o.Status == history.StatusReady) {
			target = o
			stepIdx = i
			break
		}
	}
	if target == nil && len(p.Directive.Objectives) > 0 {
		target = p.Directive.Objectives[0]
	}
	if target == nil {
		return nil, fmt.Errorf("no objective found in plan %s", planID)
	}
	out := map[string]any{
		"plan_id":      p.Directive.ID,
		"plan_title":   p.Directive.Title,
		"step_index":   stepIdx + 1,
		"total_steps":  len(p.Directive.Objectives),
		"objective_id": target.ID,
		"description":  target.Description,
		"status":       target.Status,
		"predicate":    target.Predicate,
		"parameters":   p.Parameters,
	}
	if ex, ok := p.Exemplars[target.ID]; ok {
		out["worked_example"] = ex
	}
	return out, nil
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
