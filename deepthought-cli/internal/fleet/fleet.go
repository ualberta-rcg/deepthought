// Package fleet provides the Unicomplex/Subspace fleet compute fabric:
// Client B ↔ Server ↔ Client A discovery, remote terminal observation,
// cross-node task/prompt/tool/key/approval dispatch, and cluster-wide
// operational memory queries.
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/host"
	"deepthought-cli/internal/tools"
)

// Node describes an online DeepThought CLI instance in the user's fleet.
type Node struct {
	ClientID        string            `json:"client_id"`
	UserID          string            `json:"user_id,omitempty"`
	SystemID        string            `json:"system_id"`
	Hostname        string            `json:"hostname"`
	Cluster         string            `json:"cluster"`
	Site            string            `json:"site,omitempty"`
	OS              string            `json:"os,omitempty"`
	Arch            string            `json:"arch,omitempty"`
	CPUs            int               `json:"cpus,omitempty"`
	MemGB           int               `json:"mem_gb,omitempty"`
	Capabilities    string            `json:"capabilities,omitempty"`
	Services        map[string]string `json:"services,omitempty"`
	Mode            string            `json:"mode,omitempty"`
	ActiveModel     string            `json:"active_model,omitempty"`
	ActiveChatID    string            `json:"active_chat_id,omitempty"`
	ActiveChatTitle string            `json:"active_chat_title,omitempty"`
	Busy            bool              `json:"busy"`
	TerminalFrame   string            `json:"terminal_frame,omitempty"`
	PendingApproval *Approval         `json:"pending_approval,omitempty"`
	UpdatedAt       time.Time         `json:"updated_at,omitempty"`
}

// Approval describes a pending Queen approval prompt on a remote client.
type Approval struct {
	ProbeID   string         `json:"probe_id"`
	ToolName  string         `json:"tool_name"`
	Summary   string         `json:"summary"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// Task is a remote operation dispatched to a fleet node.
type Task struct {
	ID           string         `json:"id"`
	SourceClient string         `json:"source_client,omitempty"`
	TargetClient string         `json:"target_client"`
	Kind         string         `json:"kind"` // "prompt", "bash", "tool", "keys", "queen_approve", "interrupt"
	Payload      string         `json:"payload,omitempty"`
	ToolName     string         `json:"tool_name,omitempty"`
	Arguments    map[string]any `json:"arguments,omitempty"`
	Status       string         `json:"status"`
	Output       string         `json:"output,omitempty"`
	Error        string         `json:"error,omitempty"`
	CreatedAt    time.Time      `json:"created_at,omitempty"`
	CompletedAt  time.Time      `json:"completed_at,omitempty"`
}

// CompletedTask reports the outcome of a fleet task back to the server.
type CompletedTask struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// Client talks to the DeepThought server's /api/v1/fleet/* endpoints.
type Client struct {
	ConfigSource func() config.Config
	HostSource   func() host.Record
	HTTP         *http.Client
}

func (c *Client) endpointAndToken() (string, string, error) {
	if c == nil || c.ConfigSource == nil {
		return "", "", fmt.Errorf("fleet client not configured")
	}
	cfg := c.ConfigSource()
	var base, token string
	if cfg.Server != nil {
		base = strings.TrimRight(strings.TrimSpace(cfg.Server.URL), "/")
		token = strings.TrimSpace(cfg.Server.SessionToken)
		if token == "" {
			token = strings.TrimSpace(cfg.Server.Token)
		}
	}
	if base == "" || token == "" {
		return "", "", fmt.Errorf("not logged in to a DeepThought server (use 2P MULTIPLAYER or /sync to sign in)")
	}
	return base, token, nil
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Heartbeat pushes this node's live state + completed task results and returns any newly queued tasks.
func (c *Client) Heartbeat(ctx context.Context, node Node, completed []CompletedTask) ([]Task, error) {
	base, token, err := c.endpointAndToken()
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"client_id":         node.ClientID,
		"system_id":         node.SystemID,
		"hostname":          node.Hostname,
		"cluster":           node.Cluster,
		"site":              node.Site,
		"os":                node.OS,
		"arch":              node.Arch,
		"cpus":              node.CPUs,
		"mem_gb":            node.MemGB,
		"capabilities":      node.Capabilities,
		"services":          node.Services,
		"mode":              node.Mode,
		"active_model":      node.ActiveModel,
		"active_chat_id":    node.ActiveChatID,
		"active_chat_title": node.ActiveChatTitle,
		"busy":              node.Busy,
		"terminal_frame":    node.TerminalFrame,
		"pending_approval":  node.PendingApproval,
		"completed_tasks":   completed,
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/fleet/heartbeat", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fleet heartbeat HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Tasks, nil
}

// ListNodes returns all online fleet nodes and recent tasks for this user.
func (c *Client) ListNodes(ctx context.Context) ([]Node, []Task, error) {
	base, token, err := c.endpointAndToken()
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/fleet/nodes", nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, nil, fmt.Errorf("fleet nodes HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Nodes []Node `json:"nodes"`
		Tasks []Task `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, err
	}
	return out.Nodes, out.Tasks, nil
}

// Dispatch queues a task on a target fleet node (Client B -> Server -> Client A).
func (c *Client) Dispatch(ctx context.Context, target, kind, payload, toolName string, args map[string]any) (*Task, error) {
	base, token, err := c.endpointAndToken()
	if err != nil {
		return nil, err
	}
	var source string
	if c.HostSource != nil {
		source = c.HostSource().SystemID()
	}
	body, _ := json.Marshal(map[string]any{
		"source_client": source,
		"target_client": target,
		"kind":          kind,
		"payload":       payload,
		"tool_name":     toolName,
		"arguments":     args,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/fleet/dispatch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fleet dispatch HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Task Task `json:"task"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out.Task, nil
}

// GetTask fetches the status and output of a dispatched task.
func (c *Client) GetTask(ctx context.Context, id string) (*Task, error) {
	base, token, err := c.endpointAndToken()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/fleet/tasks/"+id, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fleet task HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Task Task `json:"task"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out.Task, nil
}

// QueryMemory queries cross-cluster operational memory (past tool executions, step exemplars, and hosts).
func (c *Client) QueryMemory(ctx context.Context, toolFilter, query string) (map[string]any, error) {
	base, token, err := c.endpointAndToken()
	if err != nil {
		return nil, err
	}
	u := base + "/api/v1/fleet/memory?tool=" + toolFilter + "&q=" + query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// Tool exposes the fleet compute fabric to the LLM agent so Client B can inspect
// and drive Client A (same user) or query cross-cluster operational memory.
type Tool struct {
	Client *Client
}

func (*Tool) Name() string   { return "fleet" }
func (*Tool) ReadOnly() bool { return false }
func (*Tool) Description() string {
	return "Interact with the user's multi-cluster Fleet compute fabric (Unicomplex): list online DeepThought clients/nodes and their capabilities, inspect another client's live terminal screen, dispatch a prompt/bash command/tool/approval to another client (Client B -> Client A), check task status, or query cross-cluster operational memory."
}

func (*Tool) Parameters() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type":                 "object",
		"required":             []string{"operation"},
		"additionalProperties": false,
		"properties": map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": []string{"nodes", "screen", "dispatch", "task_status", "approve", "memory"},
			},
			"target":    str,
			"kind":      map[string]any{"type": "string", "enum": []string{"prompt", "bash", "tool", "keys", "queen_approve", "interrupt"}},
			"payload":   str,
			"tool_name": str,
			"arguments": map[string]any{"type": "object"},
			"task_id":   str,
			"query":     str,
		},
	}
}

func (t *Tool) Run(ctx context.Context, args map[string]any) tools.Result {
	if t == nil || t.Client == nil {
		return tools.Result{IsError: true, Content: "fleet client unavailable", Summary: "fleet · unavailable"}
	}
	get := func(k string) string { v, _ := args[k].(string); return strings.TrimSpace(v) }
	op := get("operation")
	switch op {
	case "nodes":
		nodes, tasks, err := t.Client.ListNodes(ctx)
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "fleet · nodes failed"}
		}
		raw, _ := json.MarshalIndent(map[string]any{"nodes": nodes, "tasks": tasks}, "", "  ")
		return tools.Result{Content: string(raw), Summary: fmt.Sprintf("fleet · %d node(s) online", len(nodes))}
	case "screen":
		target := get("target")
		nodes, _, err := t.Client.ListNodes(ctx)
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "fleet · screen failed"}
		}
		for _, n := range nodes {
			if target == "" || strings.EqualFold(n.ClientID, target) || strings.EqualFold(n.SystemID, target) || strings.EqualFold(n.Hostname, target) || strings.EqualFold(n.Cluster, target) {
				header := fmt.Sprintf("[%s · %s · %s · model=%s · busy=%v]\n", n.Cluster, n.Hostname, n.SystemID, n.ActiveModel, n.Busy)
				return tools.Result{Content: header + n.TerminalFrame, Summary: "fleet · screen " + n.Hostname}
			}
		}
		return tools.Result{IsError: true, Content: "no matching online fleet node found for target " + target, Summary: "fleet · target not found"}
	case "dispatch":
		target := get("target")
		kind := get("kind")
		if kind == "" {
			kind = "prompt"
		}
		subArgs, _ := args["arguments"].(map[string]any)
		task, err := t.Client.Dispatch(ctx, target, kind, get("payload"), get("tool_name"), subArgs)
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "fleet · dispatch failed"}
		}
		raw, _ := json.MarshalIndent(task, "", "  ")
		return tools.Result{Content: string(raw), Summary: fmt.Sprintf("fleet · dispatched %s (%s) → %s", task.ID, kind, target)}
	case "approve":
		target := get("target")
		decision := get("payload")
		if decision == "" {
			decision = "y"
		}
		task, err := t.Client.Dispatch(ctx, target, "queen_approve", decision, "", nil)
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "fleet · approve failed"}
		}
		raw, _ := json.MarshalIndent(task, "", "  ")
		return tools.Result{Content: string(raw), Summary: fmt.Sprintf("fleet · sent approval %q → %s", decision, target)}
	case "task_status":
		task, err := t.Client.GetTask(ctx, get("task_id"))
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "fleet · task status failed"}
		}
		raw, _ := json.MarshalIndent(task, "", "  ")
		return tools.Result{Content: string(raw), Summary: fmt.Sprintf("fleet · task %s (%s)", task.ID, task.Status)}
	case "memory":
		mem, err := t.Client.QueryMemory(ctx, get("tool_name"), get("query"))
		if err != nil {
			return tools.Result{IsError: true, Content: err.Error(), Summary: "fleet · memory query failed"}
		}
		raw, _ := json.MarshalIndent(mem, "", "  ")
		return tools.Result{Content: string(raw), Summary: "fleet · cross-cluster memory"}
	default:
		return tools.Result{IsError: true, Content: "unknown fleet operation: " + op, Summary: "fleet · invalid op"}
	}
}
