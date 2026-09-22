// Package slurm provides typed scheduler operations. Site policy remains in
// skills; this package only invokes machine-readable scheduler interfaces.
package slurm

import (
	"bytes"
	"context"
	"deepthought-cli/internal/history"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	outbuf := &boundedOutput{}
	cmd.Stdout = outbuf
	cmd.Stderr = outbuf
	err := cmd.Run()
	out := outbuf.Bytes()
	if outbuf.exceeded {
		return nil, fmt.Errorf("%s: output exceeds 1 MiB", name)
	}
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

type boundedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (1 << 20) - b.Len()
	if n > remaining {
		b.exceeded = true
		p = p[:max(remaining, 0)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

type Client struct {
	Runner Runner
	Store  *history.SQLiteStore
	mu     sync.Mutex
}

func NewClient(runner Runner) *Client {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Client{Runner: runner}
}

type Job struct {
	Comment  string `json:"comment,omitempty"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	State    string `json:"state,omitempty"`
	Reason   string `json:"reason,omitempty"`
	NodeList string `json:"node_list,omitempty"`
	ExitCode string `json:"exit_code,omitempty"`
	Elapsed  string `json:"elapsed,omitempty"`
}

func (c *Client) Queue(ctx context.Context, jobID string) ([]Job, error) {
	if err := validateJobID(jobID); err != nil {
		return nil, err
	}
	user := os.Getenv("USER")
	if user == "" {
		return nil, fmt.Errorf("current user is unavailable")
	}
	args := []string{"--json", "--user", user}
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
	if err := validateJobID(jobID); err != nil {
		return nil, err
	}
	user := os.Getenv("USER")
	if user == "" {
		return nil, fmt.Errorf("current user is unavailable")
	}
	args := []string{"--json", "-X", "--user", user, "--starttime=now-7days"}
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
			Comment:  valueString(value, "comment"),
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
			for _, field := range []string{"number", "current"} {
				if nested, ok := typed[field]; ok {
					return valueString(map[string]any{"value": nested}, "value")
				}
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
	SubmissionID string
	CPUs         int
	Script       string
	Account      string
	Time         string
	Memory       string
	GRES         string
}

func (c *Client) submitCommand(ctx context.Context, request SubmitRequest) (string, error) {
	if request.Script == "" {
		return "", fmt.Errorf("slurm: script is required")
	}
	args := []string{"--parsable"}
	args = append(args, "--cpus-per-task", strconv.Itoa(request.CPUs), "--comment", "deepthought:"+request.SubmissionID, "--job-name", "deepthought-"+request.SubmissionID, "--chdir", filepath.Dir(request.Script), "--output", filepath.Join(filepath.Dir(request.Script), "slurm-%j.out"))
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
	if id == "" || validateJobID(id) != nil {
		return "", fmt.Errorf("slurm: sbatch returned no job id")
	}
	return id, nil
}

func (c *Client) Cancel(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("slurm: job id is required")
	}
	if err := validateJobID(id); err != nil {
		return err
	}
	jobs, err := c.Queue(ctx, id)
	if err != nil {
		return err
	}
	owned := false
	for _, job := range jobs {
		if job.ID == id {
			owned = true
		}
	}
	if !owned {
		return fmt.Errorf("job is not in the current user's active queue")
	}
	_, err = c.Runner.Run(ctx, "scancel", id)
	return err
}

var jobIDPattern = regexp.MustCompile(`^[0-9]+(?:_[0-9]+)?$`)

func validateJobID(id string) error {
	if id != "" && !jobIDPattern.MatchString(id) {
		return fmt.Errorf("invalid job id")
	}
	return nil
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
	ClusterName                                 string
	ClusterRunning, ClusterPending              int
	QueueKnown, GPUAllocKnown, MemoryAllocKnown bool
	FetchedAt                                   time.Time
	NodesTotal                                  int
	NodesUp                                     int
	CPUAlloc                                    int
	CPUIdle                                     int
	CPUTotal                                    int
	JobsRunning                                 int
	JobsPending                                 int
	GPUs                                        int     // total GPUs across nodes
	GPUsUsed                                    int     // allocated/used GPUs
	GPUUsable                                   int     // GPUs schedulable RIGHT NOW (host feasibility: free CPU+mem too)
	GPUType                                     string  // e.g. "l40s"
	MemTotalGB                                  int     // total cluster memory (sum of per-node MB → GB)
	MemAllocGB                                  int     // allocated memory GB
	Fairshare                                   float64 // user's fairshare on their default account (0–1)
	DefaultAccount                              string
	FairshareRows                               []FairshareRow // per-account fairshare + LevelFS (all the user's accounts)
	Storage                                     []string       // raw diskusage_report rows (already column-aligned)
	StorageRows                                 []StorageRow   // parsed home/scratch/project usage for bars
	Partitions                                  []PartitionRow
	YourJobs                                    []Job
	JobsKnown                                   bool
	Err                                         error
}

// PartitionRow is one Slurm partition line.
type PartitionRow struct {
	Name, Avail, State string
	Nodes              int
}

// FairshareRow is one account's fairshare standing, as reported by sshare.
// Fairshare is 0–1 (1 = highest priority); LevelFS is the account's
// share-to-usage ratio ("inf" or a float).
type FairshareRow struct {
	Account   string
	Fairshare float64
	LevelFS   string
}

// StorageRow is one mount's usage, parsed from df, for a bar.
type StorageRow struct {
	Label string // home, scratch, or the project account name
	Used  string // human-readable, e.g. "2.9T"
	Size  string // human-readable, e.g. "5.0T"
	Pct   int    // 0–100 capacity
}

// Snapshot queries sinfo + squeue and returns a structured cluster status. It is
// non-fatal per command: a failing sinfo still yields squeue-derived fields, and
// vice versa; Err is set only if nothing could be gathered.
var snapshotCache struct {
	sync.Mutex
	value     ClusterSnapshot
	attemptAt time.Time
	accountAt time.Time
	quotaAt   time.Time
	quota     []string
}

func Snapshot(ctx context.Context) ClusterSnapshot {
	snapshotCache.Lock()
	defer snapshotCache.Unlock()
	if time.Since(snapshotCache.attemptAt) < 5*time.Minute {
		return snapshotCache.value
	}
	value := snapshot(ctx)
	snapshotCache.attemptAt = time.Now()
	if value.Err != nil && !snapshotCache.value.FetchedAt.IsZero() {
		old := snapshotCache.value
		old.Err = value.Err
		value = old
	}
	snapshotCache.value = value
	return value
}
func snapshot(ctx context.Context) ClusterSnapshot {
	s := ClusterSnapshot{FetchedAt: time.Now()}
	runner := ExecRunner{}

	// Each node contributes once even when it belongs to multiple partitions.
	if raw, err := runner.Run(ctx, "sinfo", "--Node", "-h", "-o", "%N|%C|%t"); err == nil {
		seen := map[string]bool{}
		for _, line := range strings.Split(string(raw), "\n") {
			p := strings.Split(strings.TrimSpace(line), "|")
			if len(p) != 3 || seen[p[0]] {
				continue
			}
			seen[p[0]] = true
			s.NodesTotal++
			if isNodeUp(p[2]) {
				s.NodesUp++
			}
			cpu := strings.Split(p[1], "/")
			if len(cpu) == 4 {
				s.CPUAlloc += atoi(cpu[0])
				s.CPUIdle += atoi(cpu[1])
				s.CPUTotal += atoi(cpu[3])
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
			s.JobsKnown = true
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
				if j.State == "RUNNING" {
					s.JobsRunning++
				}
				if j.State == "PENDING" {
					s.JobsPending++
				}
			}
		}
	}

	// GPUs + memory. Prefer long format with GresUsed (Alliance reports
	// "gpu:l40s:1(IDX:1)" there); fall back to %G|%m|%e / %G|%m.
	s.gatherGPUsAndMem(ctx, runner)
	s.gatherOverview(ctx, runner)

	// GPU "usable" (host feasibility): a free GPU only counts if its node also
	// has the CPU and memory to back it. Computed per node from scontrol.
	s.GPUUsable = gatherGPUUsable(ctx, runner)

	// Fairshare on the user's default account, plus per-account rows.
	if user := os.Getenv("USER"); user != "" && time.Since(snapshotCache.accountAt) >= 15*time.Minute {
		snapshotCache.accountAt = time.Now()
		if acct := defaultAccount(ctx, runner, user); acct != "" {
			s.DefaultAccount = acct
			s.Fairshare = fairshare(ctx, runner, acct)
		}
		s.FairshareRows = gatherFairshareRows(ctx, runner, user)
	} else {
		s.DefaultAccount = snapshotCache.value.DefaultAccount
		s.Fairshare = snapshotCache.value.Fairshare
		s.FairshareRows = snapshotCache.value.FairshareRows
	}

	// Storage quotas (Alliance diskusage_report). Keep the raw rows — the tool
	// already column-aligns them, so parsing is fragile and unnecessary.
	if time.Since(snapshotCache.quotaAt) >= 15*time.Minute {
		snapshotCache.quotaAt = time.Now()
		if path, err := exec.LookPath("diskusage_report"); err == nil {
			if out, err := runner.Run(ctx, path); err == nil {
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

		snapshotCache.quota = append([]string(nil), s.Storage...)
	} else {
		s.Storage = append([]string(nil), snapshotCache.quota...)
	}
	// Parsed storage usage (home/scratch/projects) for bars, from df.
	s.StorageRows = gatherStorageRows(ctx, runner)

	if s.NodesTotal == 0 && s.CPUTotal == 0 && len(s.Partitions) == 0 && !s.JobsKnown {
		s.Err = fmt.Errorf("slurm: no status gathered (sinfo/squeue returned nothing)")
	}
	return s
}

// gatherGPUsAndMem fills GPUs / GPUsUsed / GPUType / MemTotalGB / MemAllocGB.
func (s *ClusterSnapshot) gatherGPUsAndMem(ctx context.Context, runner Runner) {
	// Alliance: GresUsed looks like "gpu:l40s:1(IDX:1)" — count is before (IDX.
	raw, err := runner.Run(ctx, "sinfo", "-h", "-O", "NodeList:80,Gres:80,GresUsed:80,Memory:12,AllocMem:12", "--Node")
	seen := map[string]bool{}
	if err == nil && strings.TrimSpace(string(raw)) != "" {
		s.GPUAllocKnown, s.MemoryAllocKnown = true, true
		var memMB, allocMB int
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// -O pads columns with spaces; split on 2+ spaces.
			cols := splitSinfoCols(line)
			if len(cols) < 5 || seen[cols[0]] {
				continue
			}
			seen[cols[0]] = true
			cols = cols[1:]
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
			} else if n, _, ok := parseGPUs(gres); ok && n > 0 {
				s.GPUAllocKnown = false
			}
			if len(cols) > 2 {
				memMB += atoi(strings.TrimSuffix(cols[2], "+"))
			}
			if len(cols) > 3 {
				value, e := strconv.Atoi(strings.TrimSuffix(cols[3], "+"))
				if e != nil || value < 0 {
					s.MemoryAllocKnown = false
				} else {
					allocMB += value
				}
			}
		}
		s.MemTotalGB = memMB / 1024
		s.MemAllocGB = allocMB / 1024
		if len(seen) == 0 {
			s.GPUAllocKnown, s.MemoryAllocKnown = false, false
		}
		return
	}

	// Fallback: classic %G|%m|%e.
	raw, err = runner.Run(ctx, "sinfo", "-h", "-o", "%N|%G|%m|%e", "--Node")
	if err != nil {
		raw, err = runner.Run(ctx, "sinfo", "-h", "-o", "%N|%G|%m", "--Node")
	}
	if err != nil {
		return
	}
	var memMB int
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 3 || seen[parts[0]] {
			continue
		}
		seen[parts[0]] = true
		parts = parts[1:]
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
	}
	s.MemTotalGB = memMB / 1024
	// %e reports free physical memory, not scheduler allocation.
	s.MemoryAllocKnown, s.GPUAllocKnown = false, false
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
	raw, err := runner.Run(ctx, "sshare", "-A", acct, "-u", os.Getenv("USER"), "-o", "Fairshare", "-n")
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

// Fairshare tier thresholds (factor → label), mirroring the cluster MOTD. A
// high factor means the scheduler ranks your jobs high.
const (
	FSBoosted = 0.80
	FSAhead   = 0.60
	FSNominal = 0.40
	FSBehind  = 0.20
	LFSHigh   = 1.25 // LevelFS above this (or inf) = good
	LFSLow    = 0.75 // LevelFS below this = bad
)

// FairshareTier maps a fairshare factor (0–1, 1 = highest priority) to a tier
// label plus a bar percent. Label is the colorblind-friendly word; the screen
// picks the color from it.
func FairshareTier(f float64) (label string, pct int) {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	pct = int(f*100 + 0.5)
	switch {
	case f >= FSBoosted:
		label = "boosted"
	case f >= FSAhead:
		label = "ahead"
	case f >= FSNominal:
		label = "nominal"
	case f >= FSBehind:
		label = "behind"
	default:
		label = "throttled"
	}
	return label, pct
}

// LevelFSTier classifies a LevelFS value ("inf" or a float string): high/inf =
// good (the account is under its share — priority headroom), low = bad.
func LevelFSTier(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if v == "inf" {
		return "good"
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return ""
	}
	switch {
	case f > LFSHigh:
		return "good"
	case f < LFSLow:
		return "bad"
	default:
		return "nominal"
	}
}

// gatherFairshareRows reads every account the user belongs to, with its
// fairshare factor and LevelFS, via sshare. Returns nil on any failure (the
// screen hides the block).
func gatherFairshareRows(ctx context.Context, runner Runner, user string) []FairshareRow {
	raw, err := runner.Run(ctx, "sshare", "-ahP", "--noheader", "-u", user, "-o", "Account,User,Fairshare,LevelFS")
	if err != nil {
		return nil
	}
	var rows []FairshareRow
	for _, line := range strings.Split(string(raw), "\n") {
		// Pipe-delimited: |account|user|fairshare|levelfs| (account may carry
		// leading spaces for hierarchy).
		f := strings.Split(line, "|")
		if len(f) < 5 {
			continue
		}
		if strings.TrimSpace(f[2]) != user {
			continue
		}
		acct := strings.TrimSpace(f[1])
		if acct == "" {
			continue
		}
		fs, _ := strconv.ParseFloat(strings.TrimSpace(f[3]), 64)
		rows = append(rows, FairshareRow{Account: acct, Fairshare: fs, LevelFS: strings.TrimSpace(f[4])})
	}
	return rows
}

// gatherGPUUsable computes how many GPUs could actually be scheduled right now:
// per node, min(free gpus, free CPU / cpus-per-gpu, free mem / mem-per-gpu),
// summed. A GPU on a node that is out of CPU or RAM is idle but not schedulable.
// Ported from the cluster MOTD's per-node TRES arithmetic. Returns 0 on failure.
func gatherGPUUsable(ctx context.Context, runner Runner) int {
	raw, err := runner.Run(ctx, "scontrol", "show", "nodes")
	if err != nil {
		return 0
	}
	usable := 0
	for _, line := range strings.Split(string(raw), "\n") {
		nrm, nam, cc, ac, cg, ag := 0, 0, 0, 0, 0, 0
		for _, tok := range strings.Fields(line) {
			switch {
			case strings.HasPrefix(tok, "RealMemory="):
				nrm = atoi(strings.TrimPrefix(tok, "RealMemory="))
			case strings.HasPrefix(tok, "AllocMem="):
				nam = atoi(strings.TrimPrefix(tok, "AllocMem="))
			case strings.HasPrefix(tok, "CfgTRES="):
				tres := strings.TrimPrefix(tok, "CfgTRES=")
				cg = gresGPU(tres)
				cc = cpuFromTRES(tres)
			case strings.HasPrefix(tok, "AllocTRES="):
				tres := strings.TrimPrefix(tok, "AllocTRES=")
				ag = gresGPU(tres)
				ac = cpuFromTRES(tres)
			}
		}
		if cg <= 0 {
			continue
		}
		u := cg - ag
		if cc > 0 {
			if v := (cc - ac) * cg / cc; v < u { // free CPU, in GPU-equivalents
				u = v
			}
		}
		if nrm > 0 {
			if w := (nrm - nam) * cg / nrm; w < u { // free memory, in GPU-equivalents
				u = w
			}
		}
		if u > 0 {
			usable += u
		}
	}
	return usable
}

// gresGPU extracts the GPU count from a TRES value like "gres/gpu=4,cpu=64".
func gresGPU(tres string) int {
	for _, part := range strings.Split(tres, ",") {
		if v, ok := strings.CutPrefix(part, "gres/gpu="); ok {
			return atoi(v)
		}
	}
	return 0
}

// cpuFromTRES extracts the CPU count from a TRES value like "gres/gpu=4,cpu=64".
func cpuFromTRES(tres string) int {
	for _, part := range strings.Split(tres, ",") {
		if v, ok := strings.CutPrefix(part, "cpu="); ok {
			return atoi(v)
		}
	}
	return 0
}

// gatherStorageRows reads df for home, scratch, and each project symlink under
// ~/projects, returning one row per mount for a bar. Returns nil on failure.
// (df -P forces one line per filesystem so long mount names can't wrap.)
func gatherStorageRows(ctx context.Context, runner Runner) []StorageRow {
	var labels, paths []string
	add := func(label, p string) {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			labels = append(labels, label)
			paths = append(paths, p)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add("home", home)
		if entries, err := os.ReadDir(filepath.Join(home, "projects")); err == nil {
			for _, e := range entries {
				if e.Type()&os.ModeSymlink == 0 {
					continue
				}
				add(e.Name(), filepath.Join(home, "projects", e.Name()))
				if len(labels) >= 7 { // home + scratch + up to 5 projects
					break
				}
			}
		}
	}
	if user := os.Getenv("USER"); user != "" {
		add("scratch", filepath.Join("/scratch", user))
	}
	if len(paths) == 0 {
		return nil
	}
	dfArgs := append([]string{"-h", "-P"}, paths...)
	raw, err := runner.Run(ctx, "df", dfArgs...)
	if err != nil {
		return nil
	}
	return parseStorageRows(raw, labels)
}

// parseStorageRows parses `df -h -P` output positionally against the labels
// gathered for each queried path (df prints one line per path, in order). Pure,
// so it's unit-testable without touching the filesystem.
func parseStorageRows(raw []byte, labels []string) []StorageRow {
	var out []StorageRow
	for i, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if i == 0 { // header: Filesystem Size Used Avail Capacity Mounted_on
			continue
		}
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		label := "?"
		if idx := i - 1; idx >= 0 && idx < len(labels) {
			label = labels[idx]
		}
		out = append(out, StorageRow{
			Label: label,
			Used:  f[2],
			Size:  f[1],
			Pct:   atoi(strings.TrimSuffix(f[4], "%")),
		})
	}
	return out
}

// atoi is a forgiving Atoi (0 on error) for sinfo/squeue numeric fields.
func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
