package slurm

import (
	"context"
	"crypto/sha256"
	"deepthought-cli/internal/history"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Submission struct {
	ID          string               `json:"id"`
	JobID       string               `json:"job_id"`
	SystemID    string               `json:"system_id,omitempty"`
	Host        string               `json:"host,omitempty"`
	Cluster     string               `json:"cluster,omitempty"`
	Site        string               `json:"site,omitempty"`
	Partition   string               `json:"partition,omitempty"`
	Account     string               `json:"account"`
	Time        string               `json:"time"`
	Memory      string               `json:"memory"`
	CPUs        int                  `json:"cpus"`
	GRES        string               `json:"gres,omitempty"`
	Script      string               `json:"script"`
	Staged      string               `json:"staged"`
	ScriptHash  string               `json:"script_hash"`
	SpecHash    string               `json:"spec_hash"`
	State       string               `json:"state"`
	SubmittedAt time.Time            `json:"submitted_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Failure     history.FailureClass `json:"failure,omitempty"`
	Scientific  string               `json:"scientific,omitempty"`
}

const (
	FailureNaNLoss          = "nan_loss"
	FailureDivergence       = "divergence"
	FailureEmptyOutput      = "empty_output"
	FailureInvalidMetric    = "invalid_metric"
	FailureUnverifiedOutput = "unverified_output"
)

var (
	submissionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	accountPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	walltimePattern   = regexp.MustCompile(`^(?:\d+-)?\d{1,2}:\d{2}:\d{2}$`)
	memoryPattern     = regexp.MustCompile(`^[1-9][0-9]*[MGT]$`)
	gresPattern       = regexp.MustCompile(`^gpu(?::[A-Za-z0-9._-]+)?(?::[1-9][0-9]*)?$`)
)

func (c *Client) Submit(ctx context.Context, request SubmitRequest) (string, error) {
	_, id, err := c.SubmitManaged(ctx, request)
	return id, err
}

func (c *Client) resolveSystemContext(req SubmitRequest) (sysID, host, cluster, site string) {
	sysID = firstNonEmpty(req.SystemID, c.SystemID)
	host = firstNonEmpty(req.Host, c.Host)
	if host == "" {
		host, _ = os.Hostname()
	}
	cluster = firstNonEmpty(req.Cluster, c.Cluster, os.Getenv("SLURM_CLUSTER_NAME"), os.Getenv("CC_CLUSTER"), os.Getenv("DRAC_CLUSTER"))
	site = firstNonEmpty(req.Site, c.Site)
	if site == "" {
		if _, err := os.Stat("/cvmfs/soft.computecanada.ca"); err == nil || os.Getenv("CC_CLUSTER") != "" {
			site = "alliance"
		}
	}
	return sysID, host, cluster, site
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (c *Client) SubmitManaged(ctx context.Context, request SubmitRequest) (*Submission, string, error) {
	if !submissionPattern.MatchString(request.SubmissionID) || request.CPUs < 1 {
		return nil, "", fmt.Errorf("submission id and positive cpus are required")
	}
	if !accountPattern.MatchString(request.Account) || !walltimePattern.MatchString(request.Time) || !memoryPattern.MatchString(request.Memory) {
		return nil, "", fmt.Errorf("account, walltime, and memory are required")
	}
	if request.GRES != "" && !gresPattern.MatchString(request.GRES) {
		return nil, "", fmt.Errorf("invalid gres specification")
	}
	sysID, hostName, clusterName, siteName := c.resolveSystemContext(request)
	isAlliance := siteName == "" || strings.EqualFold(siteName, "alliance")
	content, err := os.ReadFile(request.Script)
	if err != nil {
		return nil, "", err
	}
	if isAlliance && request.Partition == "" {
		for _, line := range strings.Split(string(content), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#SBATCH") && (strings.Contains(trimmed, "--partition") || strings.Contains(trimmed, " -p ") || strings.HasSuffix(trimmed, " -p")) {
				return nil, "", fmt.Errorf("omit partition directives; the site routes jobs automatically")
			}
		}
	}
	sum := sha256.Sum256(content)
	scriptHash := hex.EncodeToString(sum[:])
	specSum := sha256.Sum256([]byte(strings.Join([]string{request.SubmissionID, request.Account, request.Time, request.Memory, strconv.Itoa(request.CPUs), request.GRES, request.Partition, scriptHash}, "\x00")))
	specHash := hex.EncodeToString(specSum[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, err := c.loadSubmission(request.SubmissionID); err == nil {
		if existing.SpecHash != specHash {
			return nil, "", fmt.Errorf("submission id is already bound to different script or resources")
		}
		if existing.JobID != "" {
			return existing, existing.JobID, nil
		}
		if id := c.reconcileSubmission(ctx, existing); id != "" {
			return existing, id, nil
		}
		return nil, "", fmt.Errorf("submission outcome is unresolved; reconcile queue/accounting before retrying")
	}
	if err := c.verifyAccount(ctx, request.Account); err != nil {
		return nil, "", err
	}
	baseDir := strings.TrimSpace(os.Getenv("SCRATCH"))
	if baseDir == "" && !isAlliance {
		baseDir = firstNonEmpty(os.Getenv("DEEPTHOUGHT_JOBS_DIR"), os.Getenv("WORK"))
	}
	if baseDir == "" {
		return nil, "", fmt.Errorf("SCRATCH is required for job staging")
	}
	root := filepath.Join(baseDir, "deepthought-cli", "jobs", request.SubmissionID)
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, "", err
	}
	staged := filepath.Join(root, "script.sh")
	if err := os.WriteFile(staged, content, 0700); err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	sub := &Submission{
		ID:          request.SubmissionID,
		SystemID:    sysID,
		Host:        hostName,
		Cluster:     clusterName,
		Site:        siteName,
		Partition:   request.Partition,
		Account:     request.Account,
		Time:        request.Time,
		Memory:      request.Memory,
		CPUs:        request.CPUs,
		GRES:        request.GRES,
		Script:      request.Script,
		Staged:      staged,
		ScriptHash:  scriptHash,
		SpecHash:    specHash,
		State:       "INTENT",
		SubmittedAt: now,
		UpdatedAt:   now,
	}
	if err := c.saveSubmission(sub); err != nil {
		return nil, "", err
	}
	request.Script = staged
	id, err := c.submitCommand(ctx, request)
	if err != nil {
		if id := c.reconcileSubmission(ctx, sub); id != "" {
			return sub, id, nil
		}
		sub.State = "UNRESOLVED"
		sub.UpdatedAt = time.Now().UTC()
		_ = c.saveSubmission(sub)
		return nil, "", err
	}
	sub.JobID = id
	sub.State = "SUBMITTED"
	sub.UpdatedAt = time.Now().UTC()
	return sub, id, c.saveSubmission(sub)
}

func (c *Client) verifyAccount(ctx context.Context, account string) error {
	user := os.Getenv("USER")
	if user == "" {
		return fmt.Errorf("USER is required to verify Slurm account")
	}
	out, err := c.Runner.Run(ctx, "sacctmgr", "-n", "-P", "show", "assoc", "user="+user, "format=account")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(strings.Split(line, "|")[0]) == account {
			return nil
		}
	}
	return fmt.Errorf("account is not associated with current user")
}

func (c *Client) reconcileSubmission(ctx context.Context, sub *Submission) string {
	for _, lookup := range []func(context.Context, string) ([]Job, error){c.Queue, c.Accounting} {
		jobs, err := lookup(ctx, "")
		if err != nil {
			continue
		}
		for _, job := range jobs {
			if job.Comment == "deepthought:"+sub.ID || job.Name == "deepthought-"+sub.ID {
				sub.JobID = job.ID
				sub.State = job.State
				sub.UpdatedAt = time.Now().UTC()
				_ = c.saveSubmission(sub)
				return job.ID
			}
		}
	}
	return ""
}

func (c *Client) Reconcile(ctx context.Context) ([]Submission, error) {
	if c.Store == nil {
		return nil, nil
	}
	var subs []Submission
	if err := c.Store.ListRecords("submission", &subs); err != nil {
		return nil, err
	}
	_, _, curCluster, _ := c.resolveSystemContext(SubmitRequest{})
	for i := range subs {
		// Preserve state of jobs submitted on a different system or cluster.
		if c.SystemID != "" && subs[i].SystemID != "" && subs[i].SystemID != c.SystemID {
			if curCluster == "" || subs[i].Cluster == "" || !strings.EqualFold(subs[i].Cluster, curCluster) {
				continue
			}
		} else if curCluster != "" && subs[i].Cluster != "" && !strings.EqualFold(subs[i].Cluster, curCluster) {
			continue
		}
		if subs[i].JobID == "" {
			c.reconcileSubmission(ctx, &subs[i])
		}
		if subs[i].JobID == "" {
			continue
		}
		jobs, err := c.Queue(ctx, subs[i].JobID)
		if err != nil || len(jobs) == 0 {
			jobs, err = c.Accounting(ctx, subs[i].JobID)
		}
		if err != nil || len(jobs) == 0 {
			continue
		}
		subs[i].State = jobs[0].State
		subs[i].Failure, subs[i].Scientific = ClassifyFailure(jobs[0], "")
		subs[i].UpdatedAt = time.Now().UTC()
		_ = c.saveSubmission(&subs[i])
	}
	return subs, nil
}

func (c *Client) loadSubmission(id string) (*Submission, error) {
	if c.Store == nil {
		return nil, fmt.Errorf("missing submission store")
	}
	var sub Submission
	if err := c.Store.GetRecord("submission", id, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

func (c *Client) saveSubmission(sub *Submission) error {
	if c.Store == nil {
		return nil
	}
	return c.Store.PutRecord("submission", sub.ID, sub)
}

func ClassifyFailure(job Job, observation string) (history.FailureClass, string) {
	state := strings.ToUpper(job.State)
	obs := strings.ToLower(observation)
	switch {
	case strings.Contains(obs, "nan") || strings.Contains(obs, "inf"):
		return history.FailureScientific, FailureNaNLoss
	case strings.Contains(obs, "diverg"):
		return history.FailureScientific, FailureDivergence
	case strings.Contains(obs, "empty output"):
		return history.FailureScientific, FailureEmptyOutput
	case strings.Contains(obs, "invalid metric"):
		return history.FailureScientific, FailureInvalidMetric
	case strings.Contains(obs, "unverified"):
		return history.FailureScientific, FailureUnverifiedOutput
	case strings.Contains(state, "OUT_OF_MEMORY") || strings.Contains(state, "TIMEOUT"):
		return history.FailureResource, ""
	case strings.Contains(state, "NODE_FAIL") || strings.Contains(state, "BOOT_FAIL") || strings.Contains(state, "PREEMPT"):
		return history.FailureInfrastructure, ""
	case strings.Contains(state, "CANCEL"):
		return history.FailurePolicy, ""
	case strings.Contains(state, "FAIL"):
		return history.FailureExecution, ""
	default:
		return "", ""
	}
}
