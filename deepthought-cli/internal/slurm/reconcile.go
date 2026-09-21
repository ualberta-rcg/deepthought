package slurm

import (
	"context"
	"strings"
	"time"

	"deepthought-cli/internal/history"
)

type ScientificFailure string

const (
	FailureNaNLoss              ScientificFailure = "nan_loss"
	FailureNotConverged         ScientificFailure = "not_converged"
	FailureValidationRegression ScientificFailure = "validation_regression"
	FailureNumericalInstability ScientificFailure = "numerical_instability"
	FailureSeedVariance         ScientificFailure = "seed_variance"
)

func ClassifyFailure(job Job, output string) (history.FailureClass, ScientificFailure) {
	text := strings.ToLower(job.State + " " + job.Reason + " " + output)
	switch {
	case strings.Contains(text, "out_of_memory"), strings.Contains(text, "oom"), strings.Contains(text, "time limit"):
		return history.FailureResource, ""
	case strings.Contains(text, "node_fail"), strings.Contains(text, "preempt"), strings.Contains(text, "network"):
		return history.FailureTransient, ""
	case strings.Contains(text, "nan"):
		return history.FailureScientific, FailureNaNLoss
	case strings.Contains(text, "not converg"):
		return history.FailureScientific, FailureNotConverged
	case strings.Contains(text, "validation regression"):
		return history.FailureScientific, FailureValidationRegression
	case strings.Contains(text, "numerical"):
		return history.FailureScientific, FailureNumericalInstability
	case strings.Contains(text, "seed variance"):
		return history.FailureScientific, FailureSeedVariance
	default:
		return history.FailureUser, ""
	}
}

type Reconciler struct {
	Client   *Client
	Interval time.Duration
	OnChange func(Job)
	last     map[string]string
}

func (r *Reconciler) Run(ctx context.Context) error {
	if r.Interval < 3*time.Minute {
		r.Interval = 3 * time.Minute
	}
	if r.last == nil {
		r.last = map[string]string{}
	}
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		if err := r.reconcile(ctx); err != nil && ctx.Err() == nil {
			// Scheduler outages are transient; keep the resident loop alive.
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) error {
	jobs, err := r.Client.Queue(ctx, "")
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if r.last[job.ID] != job.State {
			r.last[job.ID] = job.State
			if r.OnChange != nil {
				r.OnChange(job)
			}
		}
	}
	return nil
}
