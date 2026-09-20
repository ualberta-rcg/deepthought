package cron

import (
	"bytes"
	"context"
	"os/exec"
)

// ExecRunner runs real commands, feeding stdin when non-nil and returning
// combined stdout+stderr (crontab writes "no crontab for X" to stderr).
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	return combined.Bytes(), err
}
