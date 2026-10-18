package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

var managedModelKeys = []string{
	"id", "name", "reasoning", "input", "contextWindow", "maxTokens", "cost", "thinkingLevelMap",
}

var managedFieldLabels = map[string]string{
	"id":               "ID",
	"name":             "名称",
	"reasoning":        "推理开关",
	"input":            "输入类型",
	"contextWindow":    "上下文",
	"maxTokens":        "最大输出",
	"cost":             "价格",
	"thinkingLevelMap": "思考档位",
}

type PiConfig struct {
	Path   string
	Root   map[string]any
	Exists bool
}

func LoadPiConfig(path string) (*PiConfig, error) {
	cfg := &PiConfig{Path: path, Root: map[string]any{"providers": map[string]any{}}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("读取 pi 配置失败: %w", err)
	}
	cfg.Exists = true
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(b)) == 0 {
		return cfg, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("解析 pi 配置 %s 失败: %w", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	providers, ok := root["providers"].(map[string]any)
	if !ok {
		if _, exists := root["providers"]; exists {
			return nil, fmt.Errorf("%s 中的 providers 不是对象", path)
		}
		providers = map[string]any{}
		root["providers"] = providers
	}
	cfg.Root = root
	return cfg, nil
}

func (c *PiConfig) EnsureProvider(name, baseURL, apiKey string) (map[string]any, error) {
	if name == "" {
		name = "workbuddy"
	}
	providers, ok := c.Root["providers"].(map[string]any)
	if !ok {
		providers = map[string]any{}
		c.Root["providers"] = providers
	}
	var prov map[string]any
	if raw, exists := providers[name]; exists {
		if p, ok := raw.(map[string]any); ok {
			prov = p
		} else {
			return nil, fmt.Errorf("provider %q 不是对象", name)
		}
	} else {
		prov = map[string]any{
			"name":    "WorkBuddy2API (CodeBuddy)",
			"baseUrl": strings.TrimRight(baseURL, "/") + "/v1",
			"api":     "openai-completions",
			"apiKey":  apiKey,
			"models":  []any{},
		}
		providers[name] = prov
	}

	if s, _ := prov["name"].(string); strings.TrimSpace(s) == "" {
		prov["name"] = "WorkBuddy2API (CodeBuddy)"
	}
	if s, _ := prov["baseUrl"].(string); strings.TrimSpace(s) == "" && strings.TrimSpace(baseURL) != "" {
		prov["baseUrl"] = strings.TrimRight(baseURL, "/") + "/v1"
	}
	if s, _ := prov["api"].(string); strings.TrimSpace(s) == "" {
		prov["api"] = "openai-completions"
	}
	if s, _ := prov["apiKey"].(string); strings.TrimSpace(s) == "" && apiKey != "" {
		prov["apiKey"] = apiKey
	}
	compat, ok := prov["compat"].(map[string]any)
	if !ok {
		compat = map[string]any{}
		prov["compat"] = compat
	}
	if _, exists := compat["supportsReasoningEffort"]; !exists {
		compat["supportsReasoningEffort"] = true
	}
	if _, exists := compat["maxTokensField"]; !exists {
		compat["maxTokensField"] = "max_tokens"
	}
	return prov, nil
}

type ChangeKind string

const (
	ChangeAdded     ChangeKind = "added"
	ChangeUpdated   ChangeKind = "updated"
	ChangeRemoved   ChangeKind = "removed"
	ChangeUnchanged ChangeKind = "unchanged"
)

type Change struct {
	Kind   ChangeKind
	ID     string
	Name   string
	Fields []string
}

type MergePlan struct {
	Path      string
	Provider  string
	Changes   []Change
	Warnings  []string
	Added     int
	Updated   int
	Removed   int
	Unchanged int
}

func (p *MergePlan) HasChanges() bool {
	return p.Added+p.Updated+p.Removed > 0
}

func (p *MergePlan) Summary() string {
	return fmt.Sprintf("新增 %d，更新 %d，删除 %d，未变化 %d", p.Added, p.Updated, p.Removed, p.Unchanged)
}

func (p *MergePlan) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "配置文件: %s\n", p.Path)
	fmt.Fprintf(&b, "Provider: %s\n", p.Provider)
	fmt.Fprintf(&b, "变更统计: %s\n", p.Summary())

	writeGroup := func(title string, kind ChangeKind) {
		b.WriteString("\n" + title + ":\n")
		n := 0
		for _, c := range p.Changes {
			if c.Kind != kind {
				continue
			}
			n++
			switch kind {
			case ChangeAdded:
				fmt.Fprintf(&b, "  + %s  %s\n", c.ID, c.Name)
			case ChangeUpdated:
				fields := make([]string, 0, len(c.Fields))
				for _, f := range c.Fields {
					if label, ok := managedFieldLabels[f]; ok {
						fields = append(fields, label)
					} else {
						fields = append(fields, f)
					}
				}
				fmt.Fprintf(&b, "  ~ %s  [%s]\n", c.ID, strings.Join(fields, ", "))
			case ChangeRemoved:
				fmt.Fprintf(&b, "  - %s  %s\n", c.ID, c.Name)
			}
		}
		if n == 0 {
			b.WriteString("  （无）\n")
		}
	}

	writeGroup("新增", ChangeAdded)
	writeGroup("更新", ChangeUpdated)
	writeGroup("删除", ChangeRemoved)
	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "警告: %s\n", w)
	}
	return b.String()
}

func BuildPlan(cfg *PiConfig, providerName, baseURL, apiKey string, desired []map[string]any, keepMissing bool) (*MergePlan, error) {
	prov, err := cfg.EnsureProvider(providerName, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	if providerName == "" {
		providerName = "workbuddy"
	}
	plan := &MergePlan{Path: cfg.Path, Provider: providerName}

	existing := []map[string]any{}
	if raw, ok := prov["models"].([]any); ok {
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				existing = append(existing, m)
			}
		}
	}

	existingByID := make(map[string]map[string]any, len(existing))
	for _, m := range existing {
		id, _ := m["id"].(string)
		if id != "" {
			existingByID[id] = m
		}
	}

	desiredByID := make(map[string]map[string]any, len(desired))
	deduped := make([]map[string]any, 0, len(desired))
	for _, d := range desired {
		id, _ := d["id"].(string)
		if strings.TrimSpace(id) == "" {
			plan.Warnings = append(plan.Warnings, "跳过缺少 id 的模型")
			continue
		}
		if _, dup := desiredByID[id]; dup {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("跳过重复模型: %s", id))
			continue
		}
		desiredByID[id] = d
		deduped = append(deduped, d)
	}

	final := make([]any, 0, len(deduped)+len(existing))
	for _, d := range deduped {
		id, _ := d["id"].(string)
		if old, ok := existingByID[id]; ok {
			merged := mergeManaged(old, d)
			fields := changedFields(old, d)
			if len(fields) > 0 {
				plan.Updated++
				plan.Changes = append(plan.Changes, Change{Kind: ChangeUpdated, ID: id, Name: displayNameFromMap(d), Fields: fields})
			} else {
				plan.Unchanged++
				plan.Changes = append(plan.Changes, Change{Kind: ChangeUnchanged, ID: id, Name: displayNameFromMap(d)})
			}
			final = append(final, merged)
		} else {
			plan.Added++
			plan.Changes = append(plan.Changes, Change{Kind: ChangeAdded, ID: id, Name: displayNameFromMap(d)})
			final = append(final, d)
		}
	}

	for _, old := range existing {
		id, _ := old["id"].(string)
		if id == "" {
			if keepMissing {
				final = append(final, old)
			}
			continue
		}
		if _, ok := desiredByID[id]; ok {
			continue
		}
		if keepMissing {
			final = append(final, old)
			continue
		}
		plan.Removed++
		plan.Changes = append(plan.Changes, Change{Kind: ChangeRemoved, ID: id, Name: displayNameFromMap(old)})
	}

	prov["models"] = final
	return plan, nil
}

func mergeManaged(old, new map[string]any) map[string]any {
	out := make(map[string]any, len(old)+len(new))
	for k, v := range old {
		out[k] = v
	}
	for _, k := range managedModelKeys {
		delete(out, k)
	}
	for k, v := range new {
		out[k] = v
	}
	return out
}

func changedFields(old, new map[string]any) []string {
	var fields []string
	for _, k := range managedModelKeys {
		ov, ook := old[k]
		nv, nok := new[k]
		if !ook && !nok {
			continue
		}
		if !ook || !nok {
			fields = append(fields, k)
			continue
		}
		if !jsonEqual(ov, nv) {
			fields = append(fields, k)
		}
	}
	return fields
}

func jsonEqual(a, b any) bool {
	return reflect.DeepEqual(normalizeJSON(a), normalizeJSON(b))
}

func normalizeJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	case float64:
		return x
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeJSON(x[i])
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i := range x {
			out[i] = x[i]
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = normalizeJSON(val)
		}
		return out
	default:
		return v
	}
}

func displayNameFromMap(m map[string]any) string {
	name, _ := m["name"].(string)
	if strings.TrimSpace(name) == "" {
		name, _ = m["id"].(string)
	}
	return name
}

func (c *PiConfig) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(c.Root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化 pi 配置失败: %w", err)
	}
	return append(b, '\n'), nil
}

func AtomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".models.json.tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("同步临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		// Windows can refuse replacing an existing file.
		_ = os.Remove(path)
		if err2 := os.Rename(tmpName, path); err2 != nil {
			return fmt.Errorf("替换 %s 失败: %v（回退也失败: %v）", path, err, err2)
		}
	}
	return nil
}
