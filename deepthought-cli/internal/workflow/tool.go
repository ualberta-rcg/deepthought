package workflow

import (
	"context"
	"deepthought-cli/internal/tools"
	"encoding/json"
	"fmt"
)

type Tool struct{ Service *Service }

func (*Tool) Name() string   { return "workflow" }
func (*Tool) ReadOnly() bool { return false }
func (*Tool) Description() string {
	return "Persist a sequential scientific plan; list/get plans, link approved Slurm submissions to objectives, validate real result files or scheduler completion, and track artifact hashes. Parameter changes mark downstream artifacts stale. Never submits jobs; use slurm_submit with approval first."
}
func (*Tool) Parameters() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{"type": "object", "required": []string{"operation"}, "additionalProperties": false, "properties": map[string]any{
		"operation": map[string]any{"type": "string", "enum": []string{"create", "list", "get", "link", "validate", "parameters"}}, "id": str, "title": str, "objective_id": str, "submission_id": str, "artifact_path": str, "storage_tier": str, "parameters": map[string]any{"type": "object"},
		"steps": map[string]any{"type": "array", "minItems": 1, "maxItems": 32, "items": map[string]any{"type": "object", "required": []string{"description", "predicate"}, "properties": map[string]any{"description": str, "predicate": map[string]any{"type": "object", "required": []string{"kind"}, "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"exit_zero", "file_exists", "hash_matches", "regex_in", "numeric_bound", "model_judge"}}, "path": str, "expected": str, "pattern": str, "min": map[string]any{"type": "number"}, "max": map[string]any{"type": "number"}}}}}}}}
}
func (t *Tool) Run(ctx context.Context, args map[string]any) tools.Result {
	get := func(key string) string { v, _ := args[key].(string); return v }
	params, _ := args["parameters"].(map[string]any)
	var result any
	var err error
	switch get("operation") {
	case "list":
		result, err = t.Service.List()
	case "get":
		result, err = t.Service.Get(get("id"))
	case "create":
		raw, e := json.Marshal(args["steps"])
		if e != nil {
			err = e
			break
		}
		var steps []Step
		if err = json.Unmarshal(raw, &steps); err == nil {
			result, err = t.Service.Create(get("title"), params, steps)
		}
	case "link":
		result, err = t.Service.Link(get("id"), get("objective_id"), get("submission_id"))
	case "validate":
		result, err = t.Service.Validate(ctx, get("id"), get("objective_id"), get("artifact_path"), get("storage_tier"))
	case "parameters":
		result, err = t.Service.Reparameter(get("id"), params)
	default:
		err = fmt.Errorf("unknown workflow operation")
	}
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error(), Summary: "workflow · failed"}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error(), Summary: "workflow · failed"}
	}
	return tools.Result{Content: string(raw), Summary: "workflow · " + get("operation")}
}
