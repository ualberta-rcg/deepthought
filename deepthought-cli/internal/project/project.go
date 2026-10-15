// Package project manages SQL-backed Projects and Project Assets (datasets,
// repositories, scripts, containers, checkpoints, and output directories)
// across local SQLite and remote MySQL server sync.
package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/tools"
)

// Asset is one tracked file, dataset, script, container, or directory inside a Project.
type Asset struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Kind        string            `json:"kind"` // "dataset", "script", "repo", "container", "checkpoint", "artifact", "doc"
	Path        string            `json:"path"`
	SystemID    string            `json:"system_id,omitempty"`
	Cluster     string            `json:"cluster,omitempty"`
	StorageTier string            `json:"storage_tier,omitempty"` // "scratch", "project", "home", "cvmfs", "s3", "gcs"
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	AddedAt     string            `json:"added_at"`
}

// Project groups related chats, scientific pipelines, skills, and assets across clusters.
type Project struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	RootDir     string            `json:"root_dir,omitempty"`
	Clusters    []string          `json:"clusters,omitempty"`
	Skills      []string          `json:"skills,omitempty"`
	Pipelines   []string          `json:"pipelines,omitempty"`
	Assets      []Asset           `json:"assets,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Active      bool              `json:"active"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
}

// Service manages projects in SQLite and optionally mirrors them to the DeepThought server.
type Service struct {
	Store        *history.SQLiteStore
	ConfigSource func() config.Config
	SystemID     string
	Cluster      string
}

func (s *Service) List() ([]Project, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("project store unavailable")
	}
	rows, err := s.Store.Records("project")
	if err != nil {
		return nil, err
	}
	var out []Project
	for _, r := range rows {
		var p Project
		if json.Unmarshal(r.Data, &p) == nil && p.ID != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Service) Get(idOrName string) (*Project, error) {
	idOrName = strings.TrimSpace(idOrName)
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == idOrName || strings.EqualFold(list[i].Name, idOrName) {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("project %q not found", idOrName)
}

func (s *Service) Active() (*Project, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Active {
			return &list[i], nil
		}
	}
	if len(list) > 0 {
		return &list[0], nil
	}
	return nil, nil
}

func (s *Service) Save(p *Project) error {
	if s == nil || s.Store == nil {
		return fmt.Errorf("project store unavailable")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if err := s.Store.PutRecord("project", p.ID, p); err != nil {
		return err
	}
	go s.pushToServer(*p)
	return nil
}

func (s *Service) Create(name, description, rootDir string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name is required")
	}
	if rootDir == "" {
		if wd, err := os.Getwd(); err == nil {
			rootDir = wd
		}
	}
	slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	id := "proj_" + history.HashParameters(map[string]any{"name": slug})[:10]
	var clusters []string
	if s.Cluster != "" {
		clusters = []string{s.Cluster}
	}
	p := &Project{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(description),
		RootDir:     rootDir,
		Clusters:    clusters,
		Active:      true,
	}
	// Deactivate other projects when creating a new active one.
	if existing, err := s.List(); err == nil {
		for i := range existing {
			if existing[i].Active && existing[i].ID != p.ID {
				existing[i].Active = false
				_ = s.Store.PutRecord("project", existing[i].ID, existing[i])
			}
		}
	}
	if err := s.Save(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) Activate(idOrName string) (*Project, error) {
	target, err := s.Get(idOrName)
	if err != nil {
		return nil, err
	}
	list, _ := s.List()
	for i := range list {
		active := list[i].ID == target.ID
		if list[i].Active != active {
			list[i].Active = active
			_ = s.Save(&list[i])
		}
	}
	target.Active = true
	return target, s.Save(target)
}

func (s *Service) AddAsset(projectIDOrName string, asset Asset) (*Project, error) {
	var p *Project
	var err error
	if strings.TrimSpace(projectIDOrName) == "" {
		p, err = s.Active()
	} else {
		p, err = s.Get(projectIDOrName)
	}
	if err != nil || p == nil {
		return nil, fmt.Errorf("no target project found (create one first)")
	}
	if asset.Path != "" {
		if abs, e := filepath.Abs(asset.Path); e == nil {
			asset.Path = abs
		}
	}
	if asset.Name == "" && asset.Path != "" {
		asset.Name = filepath.Base(asset.Path)
	}
	if asset.Kind == "" {
		asset.Kind = "dataset"
	}
	if asset.SystemID == "" {
		asset.SystemID = s.SystemID
	}
	if asset.Cluster == "" {
		asset.Cluster = s.Cluster
	}
	if asset.ID == "" {
		asset.ID = "asset_" + history.HashParameters(map[string]any{"name": asset.Name, "path": asset.Path})[:10]
	}
	asset.AddedAt = time.Now().UTC().Format(time.RFC3339)

	replaced := false
	for i := range p.Assets {
		if p.Assets[i].ID == asset.ID || (asset.Path != "" && p.Assets[i].Path == asset.Path) {
			p.Assets[i] = asset
			replaced = true
			break
		}
	}
	if !replaced {
		p.Assets = append(p.Assets, asset)
	}
	return p, s.Save(p)
}

func (s *Service) pushToServer(p Project) {
	if s.ConfigSource == nil {
		return
	}
	cfg := s.ConfigSource()
	if cfg.Server == nil {
		return
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.Server.URL), "/")
	token := strings.TrimSpace(cfg.Server.SessionToken)
	if token == "" {
		token = strings.TrimSpace(cfg.Server.Token)
	}
	if base == "" || token == "" {
		return
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/api/v1/projects/"+p.ID, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
	if err == nil && resp != nil {
		resp.Body.Close()
	}
}

// Tool exposes SQL-backed project & asset management to the LLM agent.
type Tool struct {
	Service *Service
}

func (*Tool) Name() string   { return "project" }
func (*Tool) ReadOnly() bool { return false }
func (*Tool) Description() string {
	return "Manage SQL-backed Projects and Project Assets (datasets, scripts, repositories, containers, checkpoints, and output directories) across clusters. Operations: list, get, create, activate, add_asset."
}

func (*Tool) Parameters() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type":                 "object",
		"required":             []string{"operation"},
		"additionalProperties": false,
		"properties": map[string]any{
			"operation":    map[string]any{"type": "string", "enum": []string{"list", "get", "create", "activate", "add_asset"}},
			"id":           str,
			"name":         str,
			"description":  str,
			"root_dir":     str,
			"asset_name":   str,
			"asset_kind":   str,
			"asset_path":   str,
			"storage_tier": str,
		},
	}
}

func (t *Tool) Run(_ context.Context, args map[string]any) tools.Result {
	if t == nil || t.Service == nil {
		return tools.Result{IsError: true, Content: "project service unavailable", Summary: "project · unavailable"}
	}
	get := func(k string) string { v, _ := args[k].(string); return strings.TrimSpace(v) }
	op := get("operation")
	var out any
	var err error
	switch op {
	case "list":
		out, err = t.Service.List()
	case "get":
		id := get("id")
		if id == "" {
			id = get("name")
		}
		if id == "" {
			out, err = t.Service.Active()
		} else {
			out, err = t.Service.Get(id)
		}
	case "create":
		out, err = t.Service.Create(get("name"), get("description"), get("root_dir"))
	case "activate":
		id := get("id")
		if id == "" {
			id = get("name")
		}
		out, err = t.Service.Activate(id)
	case "add_asset":
		out, err = t.Service.AddAsset(get("id"), Asset{
			Name:        get("asset_name"),
			Kind:        get("asset_kind"),
			Path:        get("asset_path"),
			StorageTier: get("storage_tier"),
			Description: get("description"),
		})
	default:
		err = fmt.Errorf("unknown project operation: %s", op)
	}
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error(), Summary: "project · failed"}
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return tools.Result{Content: string(raw), Summary: "project · " + op}
}
