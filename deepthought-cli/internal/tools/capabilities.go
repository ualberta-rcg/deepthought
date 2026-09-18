package tools

import "context"

// ArgumentValidator is an optional tool capability: validate parsed arguments
// without executing the tool.
type ArgumentValidator interface {
	ValidateArgs(args map[string]any) error
}

// InputParser is an optional tool capability: turn the generic argument map
// into a typed value. History can store the typed value as the probe extension.
type InputParser interface {
	ParseArgs(args map[string]any) (any, error)
}

// TypedRunner is an optional tool capability: execute the tool with a typed
// input value produced by InputParser.
type TypedRunner interface {
	RunTyped(ctx context.Context, input any) (any, error)
}

// ResultMapper is an optional tool capability: convert a typed result into a
// model-facing string for the wire.
type ResultMapper interface {
	MapResultToString(result any) (string, error)
}
