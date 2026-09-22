package slurm

import (
	"context"
	"os"
	"strings"
)

func (s *ClusterSnapshot) gatherOverview(ctx context.Context, runner Runner) {
	// State only: never request other users' names, job IDs, or commands.
	if raw, err := runner.Run(ctx, "squeue", "--noheader", "--format=%T", "--states=RUNNING,PENDING"); err == nil {
		s.QueueKnown = true
		for _, line := range strings.Split(string(raw), "\n") {
			switch strings.TrimSpace(line) {
			case "RUNNING":
				s.ClusterRunning++
			case "PENDING":
				s.ClusterPending++
			case "":
			default:
				s.QueueKnown = false
			}
		}
	}
	s.ClusterName = os.Getenv("SLURM_CLUSTER_NAME")
	if s.ClusterName == "" {
		if raw, err := runner.Run(ctx, "scontrol", "show", "config"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				key, value, ok := strings.Cut(line, "=")
				if ok && strings.TrimSpace(key) == "ClusterName" {
					s.ClusterName = strings.TrimSpace(value)
					break
				}
			}
		}
	}
	s.ClusterName = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s.ClusterName)
}
