package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GatewayConfig struct {
	Listen string `json:"listen"`
	APIKey string `json:"api_key"`
}

func ReadGatewayConfig(path string) (GatewayConfig, error) {
	var cfg GatewayConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("读取网关配置失败: %w", err)
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("解析网关配置失败: %w", err)
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if strings.TrimSpace(cfg.Listen) == "" {
		cfg.Listen = ":7863"
	}
	return cfg, nil
}

func GatewayRootFromListen(listen string) string {
	s := strings.TrimSpace(listen)
	if s == "" {
		s = ":7863"
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		if p, e := strconv.Atoi(s); e == nil {
			host, port = "", strconv.Itoa(p)
		} else if !strings.Contains(s, ":") {
			host, port = s, "7863"
		} else {
			host, port = "", "7863"
		}
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	if port == "" {
		port = "7863"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func NormalizeGatewayRoot(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("网关地址为空")
	}
	if strings.HasPrefix(s, ":") {
		s = "127.0.0.1" + s
	} else if isAllDigits(s) {
		s = "127.0.0.1:" + s
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("无效的网关地址 %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("无效的网关地址 %q", raw)
	}
	p := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/v1/models", "/panel/api/models", "/v1", "/panel/api"} {
		if strings.HasSuffix(p, suffix) {
			p = strings.TrimSuffix(p, suffix)
			break
		}
	}
	if p == "/" {
		p = ""
	}
	u.Path = p
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func FindGatewayConfig() string {
	seen := map[string]bool{}
	var candidates []string
	add := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		if seen[abs] {
			return
		}
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			return
		}
		seen[abs] = true
		candidates = append(candidates, abs)
	}

	add(os.Getenv("WB2API_CONFIG"))

	if exe, err := os.Executable(); err == nil {
		add(filepath.Join(filepath.Dir(exe), "config.json"))
	}

	home, _ := os.UserHomeDir()
	patterns := []string{
		filepath.Join(home, "Desktop", "wb2api-panel-*", "config.json"),
		filepath.Join(home, "Desktop", "wb2api*", "config.json"),
	}
	var matches []string
	for _, pattern := range patterns {
		if m, err := filepath.Glob(pattern); err == nil {
			matches = append(matches, m...)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		si, ei := os.Stat(matches[i])
		sj, ej := os.Stat(matches[j])
		if ei != nil || ej != nil {
			return matches[i] < matches[j]
		}
		return si.ModTime().After(sj.ModTime())
	})
	for _, m := range matches {
		add(m)
	}

	add("config.json")

	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

type LogFunc func(format string, args ...any)

func logfOrNoop(logf LogFunc, format string, args ...any) {
	if logf != nil {
		logf(format, args...)
	}
}

type FetchResult struct {
	Models     []Model
	V1Count    int
	PanelCount int
	PanelOK    bool
	Excluded   []string
}

func FetchModels(ctx context.Context, root, apiKey string, usePanel bool, logf LogFunc) (*FetchResult, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	res := &FetchResult{}

	v1, v1Err := fetchV1(ctx, client, root, apiKey)
	if v1Err != nil {
		logfOrNoop(logf, "GET /v1/models 失败: %v", v1Err)
		if !usePanel {
			return nil, v1Err
		}
		panel, panelErr := fetchPanel(ctx, client, root, apiKey)
		if panelErr != nil {
			return nil, fmt.Errorf("获取模型失败：/v1/models: %v；/panel/api/models: %v", v1Err, panelErr)
		}
		logfOrNoop(logf, "/panel/api/models 成功，回退使用该列表")
		res.Models = panel
		res.PanelCount = len(panel)
		res.PanelOK = true
	} else {
		res.V1Count = len(v1)
		res.Models = v1
		logfOrNoop(logf, "/v1/models 获取成功: %d 个模型", len(v1))

		if usePanel {
			panel, panelErr := fetchPanel(ctx, client, root, apiKey)
			if panelErr != nil {
				logfOrNoop(logf, "警告: /panel/api/models 补充失败，继续使用 /v1/models: %v", panelErr)
			} else {
				res.PanelCount = len(panel)
				res.PanelOK = true
				merged, extra := mergeModels(v1, panel)
				res.Models = merged
				if extra > 0 {
					logfOrNoop(logf, "提示: /panel/api/models 有 %d 个模型不在 /v1/models 中，已忽略", extra)
				}
				logfOrNoop(logf, "/panel/api/models 获取成功: %d 个模型，已用于补充字段", len(panel))
			}
		}
	}

	filtered := make([]Model, 0, len(res.Models))
	for _, m := range res.Models {
		if m.ID == "cn:auto" {
			res.Excluded = append(res.Excluded, m.ID)
			continue
		}
		filtered = append(filtered, m)
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("网关没有返回可用模型")
	}
	res.Models = filtered
	return res, nil
}

func mergeModels(v1, panel []Model) ([]Model, int) {
	merged := append([]Model(nil), v1...)
	index := make(map[string]int, len(merged))
	for i := range merged {
		index[merged[i].ID] = i
	}
	extra := 0
	for i := range panel {
		if idx, ok := index[panel[i].ID]; ok {
			merged[idx].MergePanel(&panel[i])
		} else {
			extra++
		}
	}
	return merged, extra
}

type v1Response struct {
	Data []Model `json:"data"`
}

type panelResponse struct {
	OK     bool    `json:"ok"`
	Models []Model `json:"models"`
	Error  string  `json:"error"`
}

func fetchV1(ctx context.Context, client *http.Client, root, apiKey string) ([]Model, error) {
	var resp v1Response
	if err := getJSON(ctx, client, root+"/v1/models", apiKey, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("/v1/models 返回空列表")
	}
	return resp.Data, nil
}

func fetchPanel(ctx context.Context, client *http.Client, root, apiKey string) ([]Model, error) {
	var resp panelResponse
	if err := getJSON(ctx, client, root+"/panel/api/models", apiKey, &resp); err != nil {
		return nil, err
	}
	if len(resp.Models) == 0 {
		if resp.Error != "" {
			return nil, fmt.Errorf("/panel/api/models: %s", resp.Error)
		}
		return nil, fmt.Errorf("/panel/api/models 返回空列表")
	}
	return resp.Models, nil
}

func getJSON(ctx context.Context, client *http.Client, rawURL, apiKey string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "wb2pi-model-sync/1.0")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		return fmt.Errorf("%s 返回 HTTP %d: %s", rawURL, resp.StatusCode, msg)
	}
	if err := json.Unmarshal(body, out); err != nil {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		return fmt.Errorf("解析 %s 响应失败: %v；响应开头: %s", rawURL, err, msg)
	}
	return nil
}

func DefaultModelsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".pi", "agent", "models.json")
	}
	return filepath.Join(home, ".pi", "agent", "models.json")
}
