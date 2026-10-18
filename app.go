package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type SyncRequest struct {
	ConfigPath  string
	BaseURL     string
	APIKey      string
	ModelsPath  string
	Provider    string
	UsePanel    bool
	KeepMissing bool
}

type Preview struct {
	Req    SyncRequest
	Config *PiConfig
	Plan   *MergePlan
}

func ResolveRequest(req SyncRequest) (SyncRequest, error) {
	req.ConfigPath = expandPath(strings.TrimSpace(req.ConfigPath))
	req.ModelsPath = expandPath(strings.TrimSpace(req.ModelsPath))
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.Provider = strings.TrimSpace(req.Provider)

	if req.ConfigPath == "" && req.BaseURL == "" {
		req.ConfigPath = FindGatewayConfig()
	}
	if req.ConfigPath != "" && (req.BaseURL == "" || req.APIKey == "") {
		cfg, err := ReadGatewayConfig(req.ConfigPath)
		if err != nil {
			return req, err
		}
		if req.BaseURL == "" {
			req.BaseURL = GatewayRootFromListen(cfg.Listen)
		}
		if req.APIKey == "" {
			req.APIKey = cfg.APIKey
		}
	}
	if req.BaseURL == "" {
		req.BaseURL = "http://127.0.0.1:7863"
	}
	root, err := NormalizeGatewayRoot(req.BaseURL)
	if err != nil {
		return req, err
	}
	req.BaseURL = root

	if req.ModelsPath == "" {
		req.ModelsPath = DefaultModelsPath()
	}
	req.ModelsPath = expandPath(req.ModelsPath)
	if req.ModelsPath == "" {
		return req, fmt.Errorf("pi 配置文件路径为空")
	}
	if req.Provider == "" {
		req.Provider = "workbuddy"
	}
	return req, nil
}

func expandPath(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~\\") || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func FetchAndMerge(req SyncRequest, logf LogFunc) (SyncRequest, *FetchResult, error) {
	r, err := ResolveRequest(req)
	if err != nil {
		return req, nil, err
	}
	logfOrNoop(logf, "网关地址: %s", r.BaseURL)
	logfOrNoop(logf, "pi 配置: %s（provider: %s）", r.ModelsPath, r.Provider)
	res, err := FetchModels(context.Background(), r.BaseURL, r.APIKey, r.UsePanel, logf)
	if err != nil {
		return r, nil, err
	}
	logfOrNoop(logf, "可用模型: %d 个（已排除 %s）", len(res.Models), strings.Join(res.Excluded, ", "))
	return r, res, nil
}

func BuildDesired(models []Model, only map[string]bool) ([]map[string]any, []string, error) {
	desired := make([]map[string]any, 0, len(models))
	var warnings []string
	seen := map[string]bool{}
	for i := range models {
		m := &models[i]
		if m.ID == "cn:auto" {
			continue
		}
		if only != nil && !only[m.ID] {
			continue
		}
		if seen[m.ID] {
			warnings = append(warnings, fmt.Sprintf("跳过重复模型: %s", m.ID))
			continue
		}
		entry, err := ToPiModel(*m)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("跳过 %s: %v", m.ID, err))
			continue
		}
		seen[m.ID] = true
		desired = append(desired, entry)
	}
	if len(desired) == 0 {
		return nil, warnings, fmt.Errorf("没有可同步的模型")
	}
	return desired, warnings, nil
}

func BuildPreview(req SyncRequest, desired []map[string]any, logf LogFunc) (*Preview, error) {
	r, err := ResolveRequest(req)
	if err != nil {
		return nil, err
	}
	if len(desired) == 0 {
		return nil, fmt.Errorf("没有选中任何模型")
	}
	logfOrNoop(logf, "读取 pi 配置: %s", r.ModelsPath)
	cfg, err := LoadPiConfig(r.ModelsPath)
	if err != nil {
		return nil, err
	}
	plan, err := BuildPlan(cfg, r.Provider, r.BaseURL, r.APIKey, desired, r.KeepMissing)
	if err != nil {
		return nil, err
	}
	return &Preview{Req: r, Config: cfg, Plan: plan}, nil
}

func (p *Preview) Apply(logf LogFunc) error {
	if !p.Plan.HasChanges() {
		return nil
	}
	data, err := p.Config.Marshal()
	if err != nil {
		return err
	}
	logfOrNoop(logf, "写入 %s …", p.Plan.Path)
	return AtomicWrite(p.Plan.Path, data)
}

func parseOnly(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[part] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
