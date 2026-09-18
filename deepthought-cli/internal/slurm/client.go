// Package slurm provides typed scheduler operations. Site policy remains in
// skills; this package only invokes machine-readable scheduler interfaces.
package slurm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

type Client struct{ Runner Runner }

func NewClient(runner Runner) *Client {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Client{Runner: runner}
}

type Job struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	State    string `json:"state,omitempty"`
	Reason   string `json:"reason,omitempty"`
	NodeList string `json:"node_list,omitempty"`
	ExitCode string `json:"exit_code,omitempty"`
	Elapsed  string `json:"elapsed,omitempty"`
}

func (c *Client) Queue(ctx context.Context, jobID string) ([]Job, error) {
	args := []string{"--json"}
	if jobID != "" {
		args = append(args, "--jobs", jobID)
	}
	raw, err := c.Runner.Run(ctx, "squeue", args...)
	if err != nil {
		return nil, err
	}
	return parseJobs(raw)
}

func (c *Client) Accounting(ctx context.Context, jobID string) ([]Job, error) {
	args := []string{"--json", "-X"}
	if jobID != "" {
		args = append(args, "--jobs", jobID)
	}
	raw, err := c.Runner.Run(ctx, "sacct", args...)
	if err != nil {
		return nil, err
	}
	return parseJobs(raw)
}

func parseJobs(raw []byte) ([]Job, error) {
	var payload struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("slurm: parse jobs JSON: %w", err)
	}
	out := make([]Job, 0, len(payload.Jobs))
	for _, value := range payload.Jobs {
		out = append(out, Job{
			ID:       valueString(value, "job_id", "jobid", "id"),
			Name:     valueString(value, "name", "job_name"),
			State:    valueString(value, "job_state", "state"),
			Reason:   valueString(value, "state_reason", "reason"),
			NodeList: valueString(value, "nodes", "node_list"),
			ExitCode: valueString(value, "exit_code"),
			Elapsed:  valueString(value, "time", "elapsed"),
		})
	}
	return out, nil
}

func valueString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			return typed
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		case []any:
			var parts []string
			for _, item := range typed {
				parts = append(parts, fmt.Sprint(item))
			}
			return strings.Join(parts, ",")
		case map[string]any:
			if set, ok := typed["set"]; ok {
				return fmt.Sprint(set)
			}
			raw, _ := json.Marshal(typed)
			return string(raw)
		default:
			return fmt.Sprint(typed)
		}
	}
	return ""
}

type SubmitRequest struct {
	Script  string
	Account string
	Time    string
	Memory  string
	GRES    string
}

func (c *Client) Submit(ctx context.Context, request SubmitRequest) (string, error) {
	if request.Script == "" {
		return "", fmt.Errorf("slurm: script is required")
	}
	args := []string{"--parsable"}
	if request.Account != "" {
		args = append(args, "--account", request.Account)
	}
	if request.Time != "" {
		args = append(args, "--time", request.Time)
	}
	if request.Memory != "" {
		args = append(args, "--mem", request.Memory)
	}
	if request.GRES != "" {
		args = append(args, "--gres", request.GRES)
	}
	args = append(args, request.Script)
	raw, err := c.Runner.Run(ctx, "sbatch", args...)
	if err != nil {
		return "", err
	}
	id := strings.Split(strings.TrimSpace(string(raw)), ";")[0]
	if id == "" {
		return "", fmt.Errorf("slurm: sbatch returned no job id")
	}
	return id, nil
}

func (c *Client) Cancel(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("slurm: job id is required")
	}
	_, err := c.Runner.Run(ctx, "scancel", id)
	return err
}

func (c *Client) GRES(ctx context.Context) ([]string, error) {
	raw, err := c.Runner.Run(ctx, "sinfo", "-h", "-o", "%G", "--Node")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			out = append(out, line)
		}
	}
	return out, nil
}

// Detected reports whether Slurm scheduler commands (sinfo + squeue) are on
// PATH. The Cluster screen and its F-key are gated on this — they only work
// where a scheduler is actually installed.
func Detected() bool {
	if _, err := exec.LookPath("sinfo"); err != nil {
		return false
	}
	_, err := exec.LookPath("squeue")
	return err == nil
}

// ClusterSnapshot is the structured scheduler/site status the Status page
// renders. Built from sinfo + squeue + sshare + diskusage_report; gathered in
// the background and refreshed every few minutes so opening the page never
// blocks on a query.
type ClusterSnapshot struct {
	FetchedAt      time.Time
	NodesTotal     int
	NodesUp        int
	CPUAlloc       int
	CPUIdle        int
	CPUTotal       int
	JobsRunning    int
	JobsPending    int
	GPUs           int    // total GPUs across nodes
	GPUsUsed       int    // allocated/used GPUs
	GPUType        string // e.g. "l40s"
	MemTotalGB     int    // total cluster memory (sum of per-node MB → GB)
	MemAllocGB     int    // allocated memory GB
	Fairshare      float64 // user's fairshare on their default account (0–1)
	DefaultAccount string
	Storage        []string // raw diskusage_report rows (already column-aligned)
	Partitions     []PartitionRow
	YourJobs       []Job
	Err            error
}

// PartitionRow is one Slurm partition line.
type PartitionRow struct {
	Name, Avail, State string
	Nodes              int
}

// Snapshot queries sinfo + squeue and returns a structured cluster status. It is
// non-fatal per command: a failing sinfo still yields squeue-derived fields, and
// vice versa; Err is set only if nothing could be gathered.
func Snapshot(ctx context.Context) ClusterSnapshot {
	s := ClusterSnapshot{FetchedAt: time.Now()}
	runner := ExecRunner{}

	// Nodes + CPUs: "%D|%C" → nodecount|A/I/O/T per node group.
	if raw, err := runner.Run(ctx, "sinfo", "-h", "-o", "%D|%C"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Split(line, "|")
			s.NodesTotal += atoi(parts[0])
			if len(parts) > 1 {
				cpu := strings.Split(parts[1], "/")
				if len(cpu) >= 4 {
					s.CPUAlloc += atoi(cpu[0])
					s.CPUIdle += atoi(cpu[1])
					s.CPUTotal += atoi(cpu[3])
				}
			}
		}
	}

	// Nodes up: "%D|%t" — count nodes whose state is not down/drain/fail/*.
	if raw, err := runner.Run(ctx, "sinfo", "-h", "-o", "%D|%t"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Split(line, "|")
			n := atoi(parts[0])
			st := ""
			if len(parts) > 1 {
				st = strings.ToLower(parts[1])
			}
			if isNodeUp(st) {
				s.NodesUp += n
			}
		}
	}

	// Cluster-wide job state counts.
	if raw, err := runner.Run(ctx, "squeue", "-h", "-o", "%T"); err == nil {
		for _, st := range strings.Fields(string(raw)) {
			switch st {
			case "RUNNING":
				s.JobsRunning++
			case "PENDING":
				s.JobsPending++
			}
		}
	}

	// Partitions: "%P|%a|%D|%t" → name|avail|nodes|state.
	if raw, err := runner.Run(ctx, "sinfo", "-h", "-o", "%P|%a|%D|%t"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			p := strings.Split(line, "|")
			if len(p) < 4 {
				continue
			}
			s.Partitions = append(s.Partitions, PartitionRow{Name: strings.TrimSuffix(p[0], "*"), Avail: p[1], Nodes: atoi(p[2]), State: p[3]})
		}
	}

	// Your jobs: id|partition|name|state|time|reason.
	if user := os.Getenv("USER"); user != "" {
		if raw, err := runner.Run(ctx, "squeue", "-h", "-o", "%i|%P|%j|%T|%M|%R", "-u", user); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				f := strings.SplitN(line, "|", 6)
				j := Job{}
				if len(f) > 0 {
					j.ID = f[0]
				}
				if len(f) > 2 {
					j.Name = f[2]
				}
				if len(f) > 3 {
					j.State = f[3]
				}
				if len(f) > 4 {
					j.Elapsed = f[4]
				}
				if len(f) > 5 {
					j.Reason = f[5]
				}
				s.YourJobs = append(s.YourJobs, j)
			}
		}
	}

	// GPUs + memory. Prefer long format with GresUsed (Alliance reports
	// "gpu:l40s:1(IDX:1)" there); fall back to %G|%m|%e / %G|%m.
	s.gatherGPUsAndMem(ctx, runner)

	// Fairshare on the user's default account.
	if user := os.Getenv("USER"); user != "" {
		if acct := defaultAccount(ctx, runner, user); acct != "" {
			s.DefaultAccount = acct
			s.Fairshare = fairshare(ctx, runner, acct)
		}
	}

	// Storage quotas (Alliance diskusage_report). Keep the raw rows — the tool
	// already column-aligns them, so parsing is fragile and unnecessary.
	if path, err := exec.LookPath("diskusage_report"); err == nil {
		if out, err := exec.CommandContext(ctx, path).CombinedOutput(); err == nil {
			for i, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
				if i == 0 || strings.TrimSpace(line) == "" {
					continue // header
				}
				s.Storage = append(s.Storage, strings.TrimSpace(line))
				if len(s.Storage) >= 8 {
					break
				}
			}
		}
	}

	if s.NodesTotal == 0 && s.CPUTotal == 0 && len(s.Partitions) == 0 {
		s.Err = fmt.Errorf("slurm: no status gathered (sinfo/squeue returned nothing)")
	}
	return s
}

// gatherGPUsAndMem fills GPUs / GPUsUsed / GPUType / MemTotalGB / MemAllocGB.
func (s *ClusterSnapshot) gatherGPUsAndMem(ctx context.Context, runner Runner) {
	// Alliance: GresUsed looks like "gpu:l40s:1(IDX:1)" — count is before (IDX.
	raw, err := runner.Run(ctx, "sinfo", "-h", "-O", "Gres:80,GresUsed:80,Memory:12,AllocMem:12", "--Node")
	if err == nil && strings.TrimSpace(string(raw)) != "" {
		var memMB, allocMB int
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// -O pads columns with spaces; split on 2+ spaces.
			cols := splitSinfoCols(line)
			gres, usedStr := "", ""
			if len(cols) > 0 {
				gres = cols[0]
			}
			if len(cols) > 1 {
				usedStr = cols[1]
			}
			if n, typ, ok := parseGPUs(gres); ok {
				s.GPUs += n
				if s.GPUType == "" {
					s.GPUType = typ
				}
			}
			if used, ok := parseGPUsUsed(usedStr); ok {
				s.GPUsUsed += used
			} else if used, ok := parseGPUsUsed(gres); ok {
				s.GPUsUsed += used
			}
			if len(cols) > 2 {
				memMB += atoi(strings.TrimSuffix(cols[2], "+"))
			}
			if len(cols) > 3 {
				allocMB += atoi(strings.TrimSuffix(cols[3], "+"))
			}
		}
		s.MemTotalGB = memMB / 1024
		s.MemAllocGB = allocMB / 1024
		return
	}

	// Fallback: classic %G|%m|%e.
	raw, err = runner.Run(ctx, "sinfo", "-h", "-o", "%G|%m|%e", "--Node")
	if err != nil {
		raw, err = runner.Run(ctx, "sinfo", "-h", "-o", "%G|%m", "--Node")
	}
	if err != nil {
		return
	}
	var memMB, allocMB int
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		gres := ""
		if len(parts) > 0 {
			gres = parts[0]
		}
		if n, typ, ok := parseGPUs(gres); ok {
			s.GPUs += n
			if s.GPUType == "" {
				s.GPUType = typ
			}
		}
		if used, ok := parseGPUsUsed(gres); ok {
			s.GPUsUsed += used
		}
		if len(parts) > 1 {
			memMB += atoi(strings.TrimSuffix(parts[1], "+"))
		}
		if len(parts) > 2 {
			allocMB += atoi(strings.TrimSuffix(parts[2], "+"))
		}
	}
	s.MemTotalGB = memMB / 1024
	s.MemAllocGB = allocMB / 1024
}

// splitSinfoCols splits an sinfo -O line on runs of 2+ spaces.
func splitSinfoCols(line string) []string {
	var cols []string
	start := -1
	spaces := 0
	flush := func(end int) {
		if start >= 0 {
			cols = append(cols, strings.TrimSpace(line[start:end]))
			start = -1
		}
		spaces = 0
	}
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' {
			if start >= 0 {
				spaces++
				if spaces >= 2 {
					flush(i - spaces + 1)
				}
			}
			continue
		}
		if start < 0 {
			start = i
		}
		spaces = 0
	}
	flush(len(line))
	return cols
}

// parseGPUs extracts (count, type) from a GRES string like "gpu:l40s:4" or
// "gpu:l40s:4,shard:l40s:16". Returns ok=false when there are no GPUs
// ("(null)" / empty).
func parseGPUs(gres string) (count int, typ string, ok bool) {
	for _, part := range strings.Split(gres, ",") {
		fields := strings.Split(strings.TrimSpace(part), ":")
		if len(fields) >= 3 && fields[0] == "gpu" {
			// Strip "(IDX:…)" suffix from the count field if present.
			last := fields[len(fields)-1]
			if i := strings.IndexByte(last, '('); i > 0 {
				last = last[:i]
			}
			n := atoi(last)
			if n > 0 {
				return n, fields[1], true
			}
		}
	}
	return 0, "", false
}

// parseGPUsUsed reads used count from GresUsed forms like "gpu:l40s:1(IDX:1)"
// or legacy "gpu:TYPE:TOTAL(USED)".
func parseGPUsUsed(gres string) (used int, ok bool) {
	for _, part := range strings.Split(gres, ",") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "gpu:") {
			continue
		}
		// Prefer count immediately before "(IDX:" / "(".
		open := strings.IndexByte(part, '(')
		if open > 0 {
			before := part[:open]
			fields := strings.Split(before, ":")
			if len(fields) >= 3 {
				return atoi(fields[len(fields)-1]), true
			}
		}
		fields := strings.Split(part, ":")
		if len(fields) >= 3 {
			return atoi(fields[len(fields)-1]), true
		}
	}
	return 0, false
}

// isNodeUp reports whether an sinfo %t state counts as "up".
func isNodeUp(st string) bool {
	st = strings.TrimRight(st, "*")
	switch {
	case st == "" || strings.HasPrefix(st, "down") || strings.HasPrefix(st, "drain") ||
		strings.HasPrefix(st, "fail") || strings.HasPrefix(st, "unk") ||
		strings.HasPrefix(st, "maint") || strings.HasPrefix(st, "not"):
		return false
	default:
		return true
	}
}

// defaultAccount resolves the user's default Slurm account (the first association).
func defaultAccount(ctx context.Context, runner Runner, user string) string {
	raw, err := runner.Run(ctx, "sacctmgr", "show", "assoc", "user="+user, "format=Account", "-n")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if a := strings.TrimSpace(line); a != "" {
			return a
		}
	}
	return ""
}

// fairshare reads the user's fairshare float on account acct via sshare. The
// leaf (user) association row is the one with a leading space; its last field is
// the fairshare.
func fairshare(ctx context.Context, runner Runner, acct string) float64 {
	raw, err := runner.Run(ctx, "sshare", "-A", acct, "-o", "Fairshare", "-n")
	if err != nil {
		return 0
	}
	var last float64
	for _, line := range strings.Split(string(raw), "\n") {
		// Leaf rows are indented (leading space); parent rows are not. Parse any
		// line whose trimmed value is a float; the leaf's wins (parsed last).
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		var f float64
		if _, err := fmt.Sscanf(t, "%f", &f); err == nil {
			last = f
		}
	}
	return last
}

// atoi is a forgiving Atoi (0 on error) for sinfo/squeue numeric fields.
func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
