package alcove

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// Envelope is the user-space cgroup policy applied through systemd-run --user.
// Zero values use conservative login-node defaults.
type Envelope struct {
	MemoryMax int64
	CPUQuota  int
	TasksMax  int
}

func DefaultEnvelope() Envelope {
	return Envelope{
		MemoryMax: 1 << 30, // 1 GiB
		CPUQuota:  100,     // one CPU
		TasksMax:  64,
	}
}

// Available reports whether a user systemd manager can create transient scopes.
func (e Envelope) Available() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemd-run", "--user", "--scope", "--quiet", "true").Run() == nil
}

// Wrap returns argv that runs command in the configured transient user scope.
func (e Envelope) Wrap(command string, args ...string) (string, []string) {
	if e.MemoryMax <= 0 {
		e.MemoryMax = 1 << 30
	}
	if e.CPUQuota <= 0 {
		e.CPUQuota = 100
	}
	if e.TasksMax <= 0 {
		e.TasksMax = 64
	}
	wrapped := []string{
		"--user", "--scope", "--quiet",
		"--property=MemoryMax=" + strconv.FormatInt(e.MemoryMax, 10),
		"--property=CPUQuota=" + fmt.Sprintf("%d%%", e.CPUQuota),
		"--property=TasksMax=" + strconv.Itoa(e.TasksMax),
		"--", command,
	}
	return "systemd-run", append(wrapped, args...)
}
