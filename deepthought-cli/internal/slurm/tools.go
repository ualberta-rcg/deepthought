package slurm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"deepthought-cli/internal/tools"
)

type ToolSet struct{ Client *Client }

func (s ToolSet) Tools() []tools.Tool {
	return []tools.Tool{
		&submitTool{s.Client}, &statusTool{s.Client}, &cancelTool{s.Client},
		&logTool{}, &allocTool{s.Client}, &gresTool{s.Client},
	}
}

type submitTool struct{ client *Client }

func (*submitTool) Name() string { return "slurm_submit" }
func (*submitTool) Description() string {
	return "Submit a self-contained script with sbatch. Jobs run scripts, never DeepThought agents or model loops."
}
func (*submitTool) ReadOnly() bool { return false }
func (*submitTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"script":  stringSchema("Absolute path to a self-contained batch script"),
		"account": stringSchema("Optional Slurm account"),
		"time":    stringSchema("Walltime; site skills define routing policy"),
		"memory":  stringSchema("Memory request, for example 32G"),
		"gres":    stringSchema("Exact discovered GRES string"),
	}, "script")
}
func (t *submitTool) Run(ctx context.Context, args map[string]any) tools.Result {
	id, err := t.client.Submit(ctx, SubmitRequest{
		Script: stringArg(args, "script"), Account: stringArg(args, "account"),
		Time: stringArg(args, "time"), Memory: stringArg(args, "memory"), GRES: stringArg(args, "gres"),
	})
	if err != nil {
		return failure("slurm_submit", err)
	}
	return tools.Result{Content: `{"job_id":` + strconv.Quote(id) + `}`, Summary: "slurm submit · " + id}
}

type statusTool struct{ client *Client }

func (*statusTool) Name() string { return "slurm_status" }
func (*statusTool) Description() string {
	return "Read current or historical Slurm job state from squeue --json and sacct --json."
}
func (*statusTool) ReadOnly() bool { return true }
func (*statusTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"job_id":  stringSchema("Optional job id"),
		"history": map[string]any{"type": "boolean"},
	})
}
func (t *statusTool) Run(ctx context.Context, args map[string]any) tools.Result {
	var jobs []Job
	var err error
	if history, _ := args["history"].(bool); history {
		jobs, err = t.client.Accounting(ctx, stringArg(args, "job_id"))
	} else {
		jobs, err = t.client.Queue(ctx, stringArg(args, "job_id"))
	}
	if err != nil {
		return failure("slurm_status", err)
	}
	raw, _ := json.Marshal(jobs)
	return tools.Result{Content: string(raw), Summary: fmt.Sprintf("slurm status · %d job(s)", len(jobs))}
}

type cancelTool struct{ client *Client }

func (*cancelTool) Name() string        { return "slurm_cancel" }
func (*cancelTool) Description() string { return "Cancel one Slurm job by id." }
func (*cancelTool) ReadOnly() bool      { return false }
func (*cancelTool) Parameters() map[string]any {
	return objectSchema(map[string]any{"job_id": stringSchema("Job id")}, "job_id")
}
func (t *cancelTool) Run(ctx context.Context, args map[string]any) tools.Result {
	id := stringArg(args, "job_id")
	if err := t.client.Cancel(ctx, id); err != nil {
		return failure("slurm_cancel", err)
	}
	return tools.Result{Content: `{"cancelled":` + strconv.Quote(id) + `}`, Summary: "slurm cancel · " + id}
}

type logTool struct{}

func (*logTool) Name() string        { return "slurm_log_tail" }
func (*logTool) Description() string { return "Read a bounded tail of a known Slurm output file." }
func (*logTool) ReadOnly() bool      { return true }
func (*logTool) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"path":  stringSchema("Absolute Slurm output path"),
		"bytes": map[string]any{"type": "integer", "maximum": 262144},
	}, "path")
}
func (*logTool) Run(_ context.Context, args map[string]any) tools.Result {
	path := stringArg(args, "path")
	if !strings.HasPrefix(path, "/") {
		return failure("slurm_log_tail", fmt.Errorf("path must be absolute"))
	}
	n := int64(64 << 10)
	if value, ok := numArg(args["bytes"]); ok && value > 0 && value <= 256<<10 {
		n = int64(value)
	}
	file, err := os.Open(path)
	if err != nil {
		return failure("slurm_log_tail", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return failure("slurm_log_tail", err)
	}
	if info.Size() > n {
		_, _ = file.Seek(-n, io.SeekEnd)
	}
	raw, err := io.ReadAll(io.LimitReader(file, n))
	if err != nil {
		return failure("slurm_log_tail", err)
	}
	return tools.Result{Content: string(raw), Summary: fmt.Sprintf("slurm log · %d bytes", len(raw))}
}

type allocTool struct{ client *Client }

func (*allocTool) Name() string               { return "slurm_allocations" }
func (*allocTool) Description() string        { return "List current allocations from squeue --json." }
func (*allocTool) ReadOnly() bool             { return true }
func (*allocTool) Parameters() map[string]any { return objectSchema(nil) }
func (t *allocTool) Run(ctx context.Context, _ map[string]any) tools.Result {
	jobs, err := t.client.Queue(ctx, "")
	if err != nil {
		return failure("slurm_allocations", err)
	}
	raw, _ := json.Marshal(jobs)
	return tools.Result{Content: string(raw), Summary: fmt.Sprintf("slurm allocations · %d", len(jobs))}
}

type gresTool struct{ client *Client }

func (*gresTool) Name() string { return "slurm_gres" }
func (*gresTool) Description() string {
	return "Discover exact node GRES inventory before constructing a GPU request."
}
func (*gresTool) ReadOnly() bool             { return true }
func (*gresTool) Parameters() map[string]any { return objectSchema(nil) }
func (t *gresTool) Run(ctx context.Context, _ map[string]any) tools.Result {
	gres, err := t.client.GRES(ctx)
	if err != nil {
		return failure("slurm_gres", err)
	}
	raw, _ := json.Marshal(gres)
	return tools.Result{Content: string(raw), Summary: fmt.Sprintf("slurm GRES · %d type(s)", len(gres))}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{
		"type": "object", "properties": properties, "required": required,
		"additionalProperties": false,
	}
}
func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}
func numArg(value any) (int, bool) {
	switch n := value.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	default:
		return 0, false
	}
}
func failure(name string, err error) tools.Result {
	return tools.Result{IsError: true, Content: err.Error(), Summary: name + " · failed"}
}
