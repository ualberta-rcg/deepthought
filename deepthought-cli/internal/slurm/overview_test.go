package slurm

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

type overviewRunner struct{ calls [][]string }

func (r *overviewRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if name == "squeue" {
		return []byte("RUNNING\nRUNNING\nPENDING\n"), nil
	}
	if name == "scontrol" {
		return []byte("ClusterName = example-cluster\nUnrelated = ignored\n"), nil
	}
	return nil, fmt.Errorf("unavailable")
}
func TestClusterOverviewRequestsOnlyAggregateStates(t *testing.T) {
	t.Setenv("SLURM_CLUSTER_NAME", "")
	runner := &overviewRunner{}
	var s ClusterSnapshot
	s.gatherOverview(context.Background(), runner)
	if s.ClusterName != "example-cluster" || !s.QueueKnown || s.ClusterRunning != 2 || s.ClusterPending != 1 {
		t.Fatalf("bad overview: %+v", s)
	}
	want := []string{"squeue", "--noheader", "--format=%T", "--states=RUNNING,PENDING"}
	if !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatal("queried more than job states")
	}
}
