package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"deepthought-cli/internal/alcove"
)

// bashDefaultTimeout caps a synchronous bash run when the model doesn't ask for
// longer. Generous enough for real work, short enough that a hung command doesn't
// pin the agent loop forever.
const bashDefaultTimeout = 30 * time.Second

// bashMaxTimeout is the ceiling on a model-requested timeout.
const bashMaxTimeout = 5 * time.Minute

// Bash runs one shell command via `bash -c`. It is NOT read-only (Queen asks before
// running it in review mode). Output is combined stdout+stderr; a non-zero exit sets
// IsError and appends "Exit code N" so the model can see the failure shape.
type Bash struct {
	shell *alcove.Shell
}

// NewBash returns the bash tool.
func NewBash() *Bash {
	return &Bash{shell: alcove.New(alcove.Options{MaxOutput: alcove.DefaultMaxOutput})}
}

// NewGuardedBash starts the persistent shell in a transient user cgroup when
// systemd user scopes are available.
func NewGuardedBash() *Bash {
	env := alcove.DefaultEnvelope()
	return &Bash{shell: alcove.New(alcove.Options{
		MaxOutput: alcove.DefaultMaxOutput,
		Envelope:  &env,
	})}
}

// NewBashWithShell binds the tool to a per-session Alcove.
func NewBashWithShell(shell *alcove.Shell) *Bash { return &Bash{shell: shell} }

func (b *Bash) Close() error { return b.shell.Close() }

// Name is the wire name the model uses in a tool_call.
func (*Bash) Name() string { return "bash" }

// Description is the model-facing prose.
func (*Bash) Description() string {
	return "Run a command in the session's persistent bash shell and return combined " +
		"stdout/stderr. cd, export, module load, conda, and venv activation persist. " +
		"Prefer targeted commands; broad recursive filesystem walks are refused."
}

// Parameters is the JSON Schema for the function arguments.
func (*Bash) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute.",
			},
			"timeout_ms": map[string]any{
				"type":        "integer",
				"description": "Optional timeout in milliseconds (max 300000).",
			},
		},
		"required":             []string{"command"},
		"additionalProperties": false,
	}
}

// ReadOnly is false: bash can mutate the world, so Queen gates it.
func (*Bash) ReadOnly() bool { return false }

// Run executes the command. The command string is required; missing/empty is an
// error result (not a panic).
func (b *Bash) Run(ctx context.Context, args map[string]any) Result {
	command, _ := args["command"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		return Result{IsError: true, Content: "bash: missing 'command' argument", Summary: "bash · missing command"}
	}
	if reason := GuardCommand(command); reason != "" {
		return Result{IsError: true, Content: "bash: refused: " + reason, Summary: "bash · filesystem guard"}
	}

	timeout := bashDefaultTimeout
	if ms, ok := numFromArgs(args["timeout_ms"]); ok && ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
		if timeout > bashMaxTimeout {
			timeout = bashMaxTimeout
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if b.shell == nil {
		b.shell = alcove.New(alcove.Options{MaxOutput: alcove.DefaultMaxOutput})
	}
	got, err := b.shell.Run(runCtx, command)

	if runCtx.Err() == context.DeadlineExceeded || err == context.DeadlineExceeded {
		return Result{
			IsError: true,
			Content: fmt.Sprintf("Command timed out after %s", timeout),
			Summary: fmt.Sprintf("bash · timed out (%s)", timeout),
		}
	}
	if err != nil {
		return Result{IsError: true, Content: err.Error(), Summary: "bash · shell failed"}
	}

	body := strings.TrimSpace(got.Output)
	if got.Truncated {
		body += fmt.Sprintf("\n[output truncated at %d bytes]", alcove.DefaultMaxOutput)
	}
	summary := "bash → exit 0"
	if got.ExitCode != 0 {
		// Non-zero exit: keep stdout but make the failure explicit for the model.
		if body != "" {
			body += "\n"
		}
		body += fmt.Sprintf("Exit code %d", got.ExitCode)
		summary = fmt.Sprintf("bash → exit %d", got.ExitCode)
		return Result{IsError: true, Content: body, Summary: summary}
	}
	if body == "" {
		body = "(no output)"
	}
	return Result{Content: body, Summary: summary}
}

// numFromArgs pulls a numeric arg that may arrive as float64 (the default for JSON
// numbers decoded into map[string]any) or an int.
func numFromArgs(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}
