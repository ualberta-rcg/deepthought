package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"deepthought-server/graph"
)

// FleetNode represents an online DeepThought CLI client connected to the server.
type FleetNode struct {
	ClientID        string            `json:"client_id"`
	UserID          string            `json:"user_id"`
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
	PendingApproval *FleetApproval    `json:"pending_approval,omitempty"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// FleetApproval represents a Queen permission gate prompt awaiting approval on a client.
type FleetApproval struct {
	ProbeID   string         `json:"probe_id"`
	ToolName  string         `json:"tool_name"`
	Summary   string         `json:"summary"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// FleetTask is a command/prompt/tool/key/approval dispatched from Client B or the Web UI to Client A.
type FleetTask struct {
	ID           string         `json:"id"`
	UserID       string         `json:"user_id"`
	SourceClient string         `json:"source_client,omitempty"`
	TargetClient string         `json:"target_client"` // ClientID, SystemID, or Cluster/Hostname
	Kind         string         `json:"kind"`          // "prompt", "bash", "tool", "keys", "queen_approve", "interrupt"
	Payload      string         `json:"payload,omitempty"`
	ToolName     string         `json:"tool_name,omitempty"`
	Arguments    map[string]any `json:"arguments,omitempty"`
	Status       string         `json:"status"` // "queued", "running", "completed", "failed"
	Output       string         `json:"output,omitempty"`
	Error        string         `json:"error,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	CompletedAt  time.Time      `json:"completed_at,omitempty"`
}

type fleetHub struct {
	mu    sync.RWMutex
	nodes map[string]*FleetNode // key: userID + "/" + clientID
	tasks map[string]*FleetTask // key: taskID
}

var globalFleet = &fleetHub{
	nodes: map[string]*FleetNode{},
	tasks: map[string]*FleetTask{},
}

func (a *API) registerFleetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/fleet/heartbeat", a.auth(a.fleetHeartbeat))
	mux.HandleFunc("GET /api/v1/fleet/nodes", a.auth(a.fleetListNodes))
	mux.HandleFunc("POST /api/v1/fleet/dispatch", a.auth(a.fleetDispatch))
	mux.HandleFunc("GET /api/v1/fleet/tasks/{id}", a.auth(a.fleetGetTask))
	mux.HandleFunc("POST /api/v1/fleet/tasks/{id}/result", a.auth(a.fleetTaskResult))
	mux.HandleFunc("GET /api/v1/fleet/memory", a.auth(a.fleetMemory))

	// Compatible aliases for Web UI / remote session attachment.
	mux.HandleFunc("GET /api/v1/client/sessions", a.auth(a.fleetListNodes))
	mux.HandleFunc("POST /api/v1/client/sessions", a.auth(a.fleetHeartbeat))
}

func (a *API) fleetHeartbeat(w http.ResponseWriter, r *http.Request) {
	user := SessionUser(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
		return
	}
	var req struct {
		FleetNode
		CompletedTasks []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Output string `json:"output"`
			Error  string `json:"error"`
		} `json:"completed_tasks,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	node := req.FleetNode
	node.UserID = user
	if node.ClientID == "" {
		node.ClientID = node.SystemID
	}
	if node.ClientID == "" {
		node.ClientID = node.Hostname
	}
	if node.ClientID == "" {
		node.ClientID = "cli-default"
	}
	node.UpdatedAt = time.Now().UTC()

	key := user + "/" + node.ClientID
	globalFleet.mu.Lock()
	if prev, ok := globalFleet.nodes[key]; ok && node.TerminalFrame == "" {
		node.TerminalFrame = prev.TerminalFrame
	}
	globalFleet.nodes[key] = &node

	for _, done := range req.CompletedTasks {
		if t, ok := globalFleet.tasks[done.ID]; ok && t.UserID == user {
			if done.Status == "" {
				done.Status = "completed"
			}
			t.Status = done.Status
			t.Output = done.Output
			t.Error = done.Error
			t.CompletedAt = time.Now().UTC()
		}
	}

	var pending []*FleetTask
	for _, t := range globalFleet.tasks {
		if t.UserID != user || t.Status != "queued" {
			continue
		}
		if matchesFleetTarget(&node, t.TargetClient) {
			t.Status = "running"
			cp := *t
			pending = append(pending, &cp)
		}
	}
	globalFleet.mu.Unlock()

	// Also persist the host snapshot in the user's records table if DB is online.
	if st, err := a.userStore(r); err == nil && node.SystemID != "" {
		_ = st.PutRecord("host-node", node.SystemID, map[string]any{
			"system_id":    node.SystemID,
			"hostname":     node.Hostname,
			"cluster":      node.Cluster,
			"site":         node.Site,
			"os":           node.OS,
			"arch":         node.Arch,
			"cpus":         node.CPUs,
			"mem_gb":       node.MemGB,
			"capabilities": node.Capabilities,
			"services":     node.Services,
			"updated_at":   node.UpdatedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"tasks":   pending,
		"node_id": node.ClientID,
	})
}

func matchesFleetTarget(node *FleetNode, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" || target == "*" || target == "any" {
		return true
	}
	return strings.EqualFold(node.ClientID, target) ||
		strings.EqualFold(node.SystemID, target) ||
		strings.EqualFold(node.Hostname, target) ||
		strings.EqualFold(node.Cluster, target)
}

func (a *API) fleetListNodes(w http.ResponseWriter, r *http.Request) {
	user := SessionUser(r)
	cutoff := time.Now().UTC().Add(-10 * time.Minute)
	globalFleet.mu.RLock()
	var nodes []*FleetNode
	var tasks []*FleetTask
	for _, n := range globalFleet.nodes {
		if n.UserID == user && n.UpdatedAt.After(cutoff) {
			cp := *n
			nodes = append(nodes, &cp)
		}
	}
	for _, t := range globalFleet.tasks {
		if t.UserID == user {
			cp := *t
			tasks = append(tasks, &cp)
		}
	}
	globalFleet.mu.RUnlock()
	if nodes == nil {
		nodes = []*FleetNode{}
	}
	if tasks == nil {
		tasks = []*FleetTask{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":    nodes,
		"sessions": nodes,
		"tasks":    tasks,
	})
}

func (a *API) fleetDispatch(w http.ResponseWriter, r *http.Request) {
	user := SessionUser(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
		return
	}
	var req struct {
		SourceClient string         `json:"source_client"`
		TargetClient string         `json:"target_client"`
		Kind         string         `json:"kind"`
		Payload      string         `json:"payload"`
		ToolName     string         `json:"tool_name"`
		Arguments    map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.Kind) == "" {
		req.Kind = "prompt"
	}
	if strings.TrimSpace(req.TargetClient) == "" {
		req.TargetClient = "any"
	}
	task := &FleetTask{
		ID:           graph.NewID("task"),
		UserID:       user,
		SourceClient: req.SourceClient,
		TargetClient: req.TargetClient,
		Kind:         req.Kind,
		Payload:      req.Payload,
		ToolName:     req.ToolName,
		Arguments:    req.Arguments,
		Status:       "queued",
		CreatedAt:    time.Now().UTC(),
	}
	globalFleet.mu.Lock()
	globalFleet.tasks[task.ID] = task
	globalFleet.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "queued",
		"task":   task,
	})
}

func (a *API) fleetGetTask(w http.ResponseWriter, r *http.Request) {
	user := SessionUser(r)
	id := strings.TrimSpace(r.PathValue("id"))
	globalFleet.mu.RLock()
	t, ok := globalFleet.tasks[id]
	var copyTask FleetTask
	if ok && t.UserID == user {
		copyTask = *t
	} else {
		ok = false
	}
	globalFleet.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "task not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": copyTask})
}

func (a *API) fleetTaskResult(w http.ResponseWriter, r *http.Request) {
	user := SessionUser(r)
	id := strings.TrimSpace(r.PathValue("id"))
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
		return
	}
	var req struct {
		Status string `json:"status"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	globalFleet.mu.Lock()
	t, ok := globalFleet.tasks[id]
	if ok && t.UserID == user {
		if req.Status == "" {
			req.Status = "completed"
		}
		t.Status = req.Status
		t.Output = req.Output
		t.Error = req.Error
		t.CompletedAt = time.Now().UTC()
	} else {
		ok = false
	}
	globalFleet.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "task not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "task_id": id})
}

// fleetMemory returns operational memory across all clusters/systems for this user:
// recent tool executions, step exemplars, and known host nodes.
func (a *API) fleetMemory(w http.ResponseWriter, r *http.Request) {
	st, err := a.userStore(r)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	toolFilter := strings.TrimSpace(r.URL.Query().Get("tool"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	execs, _ := st.ListToolExecutions("", toolFilter, 80)
	var matchedExecs []graph.ToolExecutionRecord
	for _, ex := range execs {
		if query != "" {
			hay := strings.ToLower(ex.ToolName + " " + ex.Command + " " + ex.TargetPath + " " + ex.ResponseSummary + " " + ex.ClusterName + " " + ex.SystemID)
			if !strings.Contains(hay, query) {
				continue
			}
		}
		matchedExecs = append(matchedExecs, ex)
		if len(matchedExecs) >= 25 {
			break
		}
	}
	exemplarRows, _ := st.Records("step-exemplar")
	var exemplars []any
	for _, row := range exemplarRows {
		if query != "" && !strings.Contains(strings.ToLower(string(row.Data)), query) {
			continue
		}
		var parsed any
		if json.Unmarshal(row.Data, &parsed) == nil {
			exemplars = append(exemplars, parsed)
		}
	}
	hostRows, _ := st.Records("host-node")
	var hosts []any
	for _, row := range hostRows {
		var parsed any
		if json.Unmarshal(row.Data, &parsed) == nil {
			hosts = append(hosts, parsed)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"executions": matchedExecs,
		"exemplars":  exemplars,
		"hosts":      hosts,
	})
}
