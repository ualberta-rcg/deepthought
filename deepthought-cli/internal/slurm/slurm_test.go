package slurm

import (
	"context"
	"strings"
	"testing"

	"deepthought-cli/internal/history"
)

type fakeRunner struct {
	name string
	args []string
	out  string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.name, r.args = name, args
	return []byte(r.out), nil
}

func TestMachineReadableQueueParsing(t *testing.T) {
	runner := &fakeRunner{out: `{"jobs":[
		{"job_id":123,"name":"train","job_state":["RUNNING"],"nodes":"n1"},
		{"job_id":124,"job_state":"PENDING","state_reason":"Priority"}
	]}`}
	jobs, err := NewClient(runner).Queue(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if runner.name != "squeue" || len(runner.args) == 0 || runner.args[0] != "--json" {
		t.Fatalf("invocation=%s %v", runner.name, runner.args)
	}
	if len(jobs) != 2 || jobs[0].ID != "123" || !strings.Contains(jobs[0].State, "RUNNING") {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestParseGPUsUsedAllianceIDX(t *testing.T) {
	used, ok := parseGPUsUsed("gpu:l40s:1(IDX:1)")
	if !ok || used != 1 {
		t.Fatalf("got %d ok=%v, want 1 true", used, ok)
	}
	used, ok = parseGPUsUsed("gpu:l40s:3(IDX:1-3)")
	if !ok || used != 3 {
		t.Fatalf("got %d ok=%v, want 3 true", used, ok)
	}
	used, ok = parseGPUsUsed("gpu:l40s:0(IDX:N/A)")
	if !ok || used != 0 {
		t.Fatalf("got %d ok=%v, want 0 true", used, ok)
	}
}

func TestSplitSinfoCols(t *testing.T) {
	cols := splitSinfoCols("gpu:l40s:4                                        gpu:l40s:1(IDX:1)                                 191921         49152")
	if len(cols) < 2 {
		t.Fatalf("cols=%v", cols)
	}
	if cols[0] != "gpu:l40s:4" || !strings.HasPrefix(cols[1], "gpu:l40s:1") {
		t.Fatalf("cols=%v", cols)
	}
}

func TestSubmitUsesArgumentVector(t *testing.T) {
	runner := &fakeRunner{out: "987;cluster\n"}
	id, err := NewClient(runner).submitCommand(context.Background(), SubmitRequest{
		SubmissionID: "fixture", CPUs: 2,
		Script: "/scratch/job.sh", Account: "def-x", Time: "01:00:00", Memory: "32G",
	})
	if err != nil || id != "987" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if runner.name != "sbatch" || runner.args[len(runner.args)-1] != "/scratch/job.sh" {
		t.Fatalf("args=%v", runner.args)
	}
}

func TestFailureTaxonomy(t *testing.T) {
	class, scientific := ClassifyFailure(Job{State: "FAILED"}, "loss became NaN")
	if class != history.FailureScientific || scientific != FailureNaNLoss {
		t.Fatalf("class=%s scientific=%s", class, scientific)
	}
	class, _ = ClassifyFailure(Job{State: "OUT_OF_MEMORY"}, "")
	if class != history.FailureResource {
		t.Fatalf("class=%s", class)
	}
}

func TestParseFairshareRowsFourAndFiveColumnFormats(t *testing.T) {
	raw := strings.Join([]string{
		"  aip-rahimk|rahimk|0.567300|1.000000",
		"  rrg-other-ad|rahimk|0.097723|0.500000",
		"  debug|rahimk|0.820000|inf",
		"|legacy-acct|rahimk|0.450000|1.500000|",
	}, "\n")
	rows := parseFairshareRows(raw, "rahimk")
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4: %+v", len(rows), rows)
	}
	if rows[0].Account != "aip-rahimk" || rows[0].Fairshare != 0.5673 || rows[0].LevelFS != "1.000000" {
		t.Fatalf("row[0] = %+v", rows[0])
	}
	best, ok := BestAccount(rows)
	if !ok || best.Account != "debug" || best.Fairshare != 0.82 {
		t.Fatalf("BestAccount = %+v, ok=%v", best, ok)
	}
}
