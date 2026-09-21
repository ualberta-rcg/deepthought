package slurm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"deepthought-cli/internal/tools"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Submission struct {
	ID          string        `json:"id"`
	Request     SubmitRequest `json:"request"`
	Fingerprint string        `json:"fingerprint"`
	ScriptHash  string        `json:"script_hash"`
	JobID       string        `json:"job_id,omitempty"`
	State       string        `json:"state"`
	LastError   string        `json:"last_error,omitempty"`
	Job         Job           `json:"job"`
	Created     time.Time     `json:"created"`
	Updated     time.Time     `json:"updated"`
}

var submissionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,48}$`)
var resourcePattern = regexp.MustCompile(`^[1-9][0-9]*(?:[KMGTP]?)$`)
var walltimePattern = regexp.MustCompile(`^(?:[0-9]+-)?[0-9]+(?::[0-9]{1,2}){0,2}$`)

func (c *Client) Preflight(ctx context.Context, r SubmitRequest) ([]byte, string, error) {
	if !submissionIDPattern.MatchString(r.SubmissionID) {
		return nil, "", fmt.Errorf("submission_id must be 1–48 letters, numbers, underscores or hyphens")
	}
	if r.Account == "" || r.CPUs < 1 || !walltimePattern.MatchString(r.Time) || !resourcePattern.MatchString(strings.ToUpper(r.Memory)) {
		return nil, "", fmt.Errorf("explicit account, positive CPUs, walltime and memory are required")
	}
	if !filepath.IsAbs(r.Script) || tools.SensitivePath(r.Script) {
		return nil, "", fmt.Errorf("script must be an absolute non-sensitive path")
	}
	real, err := tools.OwnedPath(r.Script)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, "", fmt.Errorf("batch script must be a regular file no larger than 1 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > 1<<20 || !strings.HasPrefix(string(raw), "#!/bin/bash") {
		return nil, "", fmt.Errorf("batch scripts must start with #!/bin/bash")
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#SBATCH") && (strings.Contains(line, "--partition") || strings.Contains(line, " -p ")) {
			return nil, "", fmt.Errorf("omit partition directives; the site routes jobs automatically")
		}
	}
	accounts, err := c.Runner.Run(ctx, "sacctmgr", "-n", "-P", "show", "assoc", "user="+os.Getenv("USER"), "format=Account")
	if err != nil {
		return nil, "", fmt.Errorf("account discovery: %w", err)
	}
	accountOK := false
	for _, line := range strings.Split(string(accounts), "\n") {
		if strings.TrimSpace(strings.Split(line, "|")[0]) == r.Account {
			accountOK = true
		}
	}
	if !accountOK {
		return nil, "", fmt.Errorf("account %q was not returned for the current user", r.Account)
	}
	if r.GRES != "" {
		parts := strings.Split(r.GRES, ":")
		if len(parts) != 3 || parts[0] != "gpu" || parts[1] == "" {
			return nil, "", fmt.Errorf("use a discovered typed GPU request, gpu:type:count")
		}
		count, err := strconv.Atoi(parts[2])
		if err != nil || count < 1 {
			return nil, "", fmt.Errorf("invalid GPU count")
		}
		inventory, err := c.GRES(ctx)
		if err != nil {
			return nil, "", err
		}
		matched := false
		for _, line := range inventory {
			for _, entry := range strings.Split(line, ",") {
				entry = strings.Split(entry, "(")[0]
				p := strings.Split(entry, ":")
				if len(p) == 3 && p[0] == "gpu" && p[1] == parts[1] {
					available, _ := strconv.Atoi(p[2])
					if count <= available {
						matched = true
					}
				}
			}
		}
		if !matched {
			return nil, "", fmt.Errorf("GPU request %q does not match discovered inventory", r.GRES)
		}
	}
	hash := sha256.Sum256(raw)
	return raw, hex.EncodeToString(hash[:]), nil
}

func (c *Client) Submit(ctx context.Context, r SubmitRequest) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Store == nil {
		return "", fmt.Errorf("submission requires a persistent journal")
	}
	raw, scriptHash, err := c.Preflight(ctx, r)
	if err != nil {
		return "", err
	}
	encoded, _ := json.Marshal(struct {
		Request SubmitRequest
		Hash    string
	}{r, scriptHash})
	sum := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(sum[:])
	var existing Submission
	err = c.Store.GetRecord("submission", r.SubmissionID, &existing)
	if err == nil {
		if existing.Fingerprint != fingerprint {
			return "", fmt.Errorf("submission_id already refers to different inputs; approve a new attempt with a new ID")
		}
		return c.resolveSubmission(ctx, &existing)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	scratch := os.Getenv("SCRATCH")
	if !filepath.IsAbs(scratch) || scratch == "/" {
		return "", fmt.Errorf("SCRATCH must name the user's scratch directory")
	}
	dir := filepath.Join(scratch, "deepthought-cli", "jobs", r.SubmissionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	staged := filepath.Join(dir, "script.sh")
	f, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		old, e := os.ReadFile(staged)
		if e != nil {
			return "", e
		}
		oldHash := sha256.Sum256(old)
		if hex.EncodeToString(oldHash[:]) != scriptHash {
			return "", fmt.Errorf("staged script differs; use a new submission_id")
		}
	} else if err != nil {
		return "", err
	} else {
		if _, err := f.Write(raw); err != nil {
			f.Close()
			return "", err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
	}
	record := Submission{ID: r.SubmissionID, Request: r, Fingerprint: fingerprint, ScriptHash: scriptHash, State: "submitting", Created: time.Now().UTC(), Updated: time.Now().UTC()}
	claimed, err := c.Store.ClaimRecord("submission", r.SubmissionID, record)
	if err != nil {
		return "", err
	}
	if !claimed {
		if err := c.Store.GetRecord("submission", r.SubmissionID, &existing); err != nil {
			return "", err
		}
		if existing.Fingerprint != fingerprint {
			return "", fmt.Errorf("submission conflict")
		}
		return c.resolveSubmission(ctx, &existing)
	}
	r.Script = staged
	id, submitErr := c.submitCommand(ctx, r)
	record.Updated = time.Now().UTC()
	if submitErr != nil {
		record.State = "unknown"
		record.LastError = submitErr.Error()
	} else {
		record.State = "submitted"
		record.JobID = id
	}
	if err := c.Store.PutRecord("submission", record.ID, record); err != nil {
		return "", fmt.Errorf("submission outcome could not be saved; reconcile before retrying: %w", err)
	}
	if submitErr != nil {
		return "", fmt.Errorf("submission outcome unknown; reuse submission_id %s to reconcile, never automatically resubmit: %w", record.ID, submitErr)
	}
	return id, nil
}
func (c *Client) resolveSubmission(ctx context.Context, s *Submission) (string, error) {
	if s.JobID != "" {
		return s.JobID, nil
	}
	queue, queueErr := c.Queue(ctx, "")
	accounting, accountErr := c.Accounting(ctx, "")
	for _, job := range append(queue, accounting...) {
		if job.Comment == "deepthought:"+s.ID || job.Name == "deepthought-"+s.ID {
			s.JobID, s.Job, s.State = job.ID, job, job.State
			s.Updated = time.Now().UTC()
			if err := c.Store.PutRecord("submission", s.ID, s); err != nil {
				return "", err
			}
			return job.ID, nil
		}
	}
	return "", fmt.Errorf("submission %s remains unresolved (queue: %v; accounting: %v); no new job was submitted", s.ID, queueErr, accountErr)
}
func (c *Client) Submissions() ([]Submission, error) {
	if c.Store == nil {
		return nil, nil
	}
	rows, err := c.Store.Records("submission")
	if err != nil {
		return nil, err
	}
	var out []Submission
	for _, row := range rows {
		var s Submission
		if err := json.Unmarshal(row.Data, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
func (c *Client) Reconcile(ctx context.Context) ([]Submission, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	items, err := c.Submissions()
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return items, nil
	}
	queue, qerr := c.Queue(ctx, "")
	accounting, aerr := c.Accounting(ctx, "")
	if qerr != nil && aerr != nil {
		return items, fmt.Errorf("scheduler unavailable: %v; %v", qerr, aerr)
	}
	jobs := map[string]Job{}
	for _, j := range accounting {
		jobs[j.ID] = j
	}
	for _, j := range queue {
		jobs[j.ID] = j
	}
	for i := range items {
		s := &items[i]
		for _, job := range jobs {
			if job.ID == s.JobID || job.Comment == "deepthought:"+s.ID || job.Name == "deepthought-"+s.ID {
				s.JobID, s.Job, s.State = job.ID, job, job.State
				s.LastError = ""
				s.Updated = time.Now().UTC()
				break
			}
		}
		if err := c.Store.PutRecord("submission", s.ID, s); err != nil {
			return items, err
		}
	}
	return items, nil
}

func RetryAdvice(s Submission) string {
	state := strings.ToUpper(s.State)
	switch {
	case strings.Contains(state, "OUT_OF_MEMORY"):
		return "Proposed retry: increase memory after inspecting MaxRSS; preserve checkpoint and use a new submission_id. Approval required."
	case strings.Contains(state, "TIMEOUT"):
		return "Proposed retry: resume a verified checkpoint with a longer walltime and a new submission_id. Approval required."
	case strings.Contains(state, "PREEMPT") || strings.Contains(state, "NODE_FAIL"):
		return "Proposed retry: verify checkpoint integrity, then submit a new approved attempt."
	case state == "UNKNOWN" || state == "SUBMITTING":
		return "Outcome unknown: reconcile the existing submission_id; do not resubmit."
	default:
		return "Inspect exit code and bounded logs before proposing changes. Retries require approval."
	}
}
