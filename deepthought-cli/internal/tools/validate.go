package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var schemas sync.Map

// Validate enforces the advertised contract before permission or execution.
func Validate(tool Tool, args map[string]any) error {
	if tool == nil {
		return fmt.Errorf("unknown tool")
	}
	return ValidateSchema(tool.Parameters(), args)
}

func ValidateSchema(parameters map[string]any, inputValue any) error {
	raw, err := json.Marshal(parameters)
	if err != nil {
		return err
	}
	key := string(raw)
	value, ok := schemas.Load(key)
	if !ok {
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("tool.json", doc); err != nil {
			return err
		}
		schema, err := compiler.Compile("tool.json")
		if err != nil {
			return fmt.Errorf("schema: %w", err)
		}
		value, _ = schemas.LoadOrStore(key, schema)
	}
	raw, err = json.Marshal(inputValue)
	if err != nil {
		return err
	}
	var input any
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	if err := value.(*jsonschema.Schema).Validate(input); err != nil {
		return fmt.Errorf("arguments: %w", err)
	}
	return nil
}

func Execute(ctx context.Context, tool Tool, args map[string]any) Result {
	if err := ctx.Err(); err != nil {
		return Result{IsError: true, Content: err.Error(), Summary: "tool · cancelled"}
	}
	if err := Validate(tool, args); err != nil {
		return Result{IsError: true, Content: err.Error(), Summary: "tool · invalid arguments"}
	}
	return tool.Run(ctx, args)
}
