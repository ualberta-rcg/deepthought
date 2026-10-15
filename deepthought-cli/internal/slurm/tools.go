package slurm

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"deepthought-cli/internal/babel"
	"deepthought-cli/internal/queen"
	"deepthought-cli/internal/tools"
)

func Tools(client *Client, Sync func(string, string, any)) []tools.Tool {
	if client == nil {
		client = NewClient(nil)
	}
	return []tools.Tool{
		&queueTool{client: client},
		&acctTool{client: client},
		&submitTool{client: client, sync: Sync},
		&cancelTool{client: client},
		&reconcileTool{client: client, sync: Sync},
	}
}

type queueTool struct{ client *Client }

func (t *queueTool) Spec() babel.ToolSpec {
	return babel.ToolSpec{Name: "slurm_queue", Description: "Inspect Slurm queue state via squeue --json.", Parameters: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string"}},"additionalProperties":false}`)}
}
func (t *queueTool) Preview(raw json.RawMessage) queen.Action {
	var in struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(raw, &in)
	return queen.Action{Tool: "slurm_queue", Summary: "squeue " + in.JobID, TargetPath: in.JobID, Category: queen.CategoryShellRead}
}
func (t *queueTool) Execute(ctx context.Context, raw json.RawMessage) (string, bool) {
	var in struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "slurm_queue: invalid arguments: " + err.Error(), true
	}
	jobs, err := t.client.Queue(ctx, in.JobID)
	if err != nil {
		return err.Error(), true
	}
	buf, _ := json.MarshalIndent(jobs, "", "  ")
	return string(buf), false
}

type acctTool struct{ client *Client }

func (t *acctTool) Spec() babel.ToolSpec {
	return babel.ToolSpec{Name: "slurm_acct", Description: "Inspect Slurm accounting records via sacct --json.", Parameters: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string"}},"additionalProperties":false}`)}
}
func (t *acctTool) Preview(raw json.RawMessage) queen.Action {
	var in struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(raw, &in)
	return queen.Action{Tool: "slurm_acct", Summary: "sacct " + in.JobID, TargetPath: in.JobID, Category: queen.CategoryShellRead}
}
func (t *acctTool) Execute(ctx context.Context, raw json.RawMessage) (string, bool) {
	var in struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "slurm_acct: invalid arguments: " + err.Error(), true
	}
	jobs, err := t.client.Accounting(ctx, in.JobID)
	if err != nil {
		return err.Error(), true
	}
	buf, _ := json.MarshalIndent(jobs, "", "  ")
	return string(buf), false
}

type submitTool struct {
	client *Client
	sync   func(string, string, any)
}

func (t *submitTool) Spec() babel.ToolSpec {
	return babel.ToolSpec{Name: "slurm_submit", Description: "Submit a batch script through sbatch after explicit resource and approval checks.", Parameters: json.RawMessage(`{"type":"object","properties":{"submission_id":{"type":"string"},"cpus":{"type":"integer"},"script":{"type":"string"},"account":{"type":"string"},"time":{"type":"string"},"memory":{"type":"string"},"gres":{"type":"string"},"partition":{"type":"string"}},"required":["submission_id","cpus","script","account","time","memory"],"additionalProperties":false}`)}
}
func (t *submitTool) Preview(raw json.RawMessage) queen.Action {
	var in struct {
		SubmissionID string `json:"submission_id"`
		CPUs         int    `json:"cpus"`
		Script       string `json:"script"`
		Account      string `json:"account"`
		Time         string `json:"time"`
		Memory       string `json:"memory"`
		GRES         string `json:"gres"`
		Partition    string `json:"partition"`
	}
	_ = json.Unmarshal(raw, &in)
	if in.SubmissionID == "" || in.CPUs < 1 || in.Script == "" || in.Account == "" || in.Time == "" || in.Memory == "" || filepath.Clean(in.Script) == "." {
		return queen.Action{Tool: "slurm_submit", Summary: "invalid slurm submission", Category: queen.CategoryDestructive}
	}
	return queen.Action{Tool: "slurm_submit", Summary: fmt.Sprintf("sbatch %s cpus=%d account=%s time=%s mem=%s gres=%s", in.Script, in.CPUs, in.Account, in.Time, in.Memory, in.GRES), TargetPath: in.Script, Category: queen.CategoryShellMutate}
}
func (t *submitTool) Execute(ctx context.Context, raw json.RawMessage) (string, bool) {
	var in struct {
		SubmissionID string `json:"submission_id"`
		CPUs         int    `json:"cpus"`
		Script       string `json:"script"`
		Account      string `json:"account"`
		Time         string `json:"time"`
		Memory       string `json:"memory"`
		GRES         string `json:"gres"`
		Partition    string `json:"partition"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "slurm_submit: invalid arguments: " + err.Error(), true
	}
	sub, id, err := t.client.SubmitManaged(ctx, SubmitRequest{SubmissionID: in.SubmissionID, CPUs: in.CPUs, Script: in.Script, Account: in.Account, Time: in.Time, Memory: in.Memory, GRES: in.GRES, Partition: in.Partition})
	if err != nil {
		return err.Error(), true
	}
	if t.sync != nil && sub != nil {
		t.sync("submission", sub.ID, sub)
	}
	return fmt.Sprintf("submitted job %s (%s) on %s [%s]", id, in.SubmissionID, firstNonEmpty(sub.Cluster, sub.Host, "local"), firstNonEmpty(sub.SystemID, "local")), false
}

type cancelTool struct{ client *Client }

func (t *cancelTool) Spec() babel.ToolSpec {
	return babel.ToolSpec{Name: "slurm_cancel", Description: "Cancel a Slurm job owned by the current user via scancel.", Parameters: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string"}},"required":["job_id"],"additionalProperties":false}`)}
}
func (t *cancelTool) Preview(raw json.RawMessage) queen.Action {
	var in struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(raw, &in)
	return queen.Action{Tool: "slurm_cancel", Summary: "scancel " + in.JobID, TargetPath: in.JobID, Category: queen.CategoryShellMutate}
}
func (t *cancelTool) Execute(ctx context.Context, raw json.RawMessage) (string, bool) {
	var in struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "slurm_cancel: invalid arguments: " + err.Error(), true
	}
	if err := t.client.Cancel(ctx, in.JobID); err != nil {
		return err.Error(), true
	}
	return "cancelled job " + in.JobID, false
}

type reconcileTool struct {
	client *Client
	sync   func(string, string, any)
}

func (t *reconcileTool) Spec() babel.ToolSpec {
	return babel.ToolSpec{Name: "slurm_reconcile", Description: "Reconcile recorded Slurm submissions against queue and accounting state.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}
func (t *reconcileTool) Preview(json.RawMessage) queen.Action {
	return queen.Action{Tool: "slurm_reconcile", Summary: "reconcile Slurm submissions", Category: queen.CategoryShellRead}
}
func (t *reconcileTool) Execute(ctx context.Context, _ json.RawMessage) (string, bool) {
	subs, err := t.client.Reconcile(ctx)
	if err != nil {
		return err.Error(), true
	}
	for _, s := range subs {
		if t.sync != nil {
			t.sync("submission", s.ID, s)
		}
	}
	buf, _ := json.MarshalIndent(subs, "", "  ")
	return string(buf), false
}
