package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig(t *testing.T) *PiConfig {
	t.Helper()
	root := map[string]any{
		"$schema": "https://pi.dev/schemas/models.schema.json",
		"providers": map[string]any{
			"other": map[string]any{
				"baseUrl": "http://example.invalid/v1",
				"api":     "openai-completions",
				"models":  []any{map[string]any{"id": "other-model"}},
			},
			"workbuddy": map[string]any{
				"name":    "Custom Name",
				"baseUrl": "http://127.0.0.1:7863/v1",
				"api":     "openai-completions",
				"apiKey":  "existing-key",
				"compat": map[string]any{
					"supportsReasoningEffort": true,
					"maxTokensField":          "max_tokens",
				},
				"models": []any{
					map[string]any{
						"id":     "cn:old",
						"name":   "Old Model",
						"custom": "keep-me",
					},
					map[string]any{
						"id":            "cn:deepseek-v4.1-flash",
						"name":          "Old Name",
						"reasoning":     true,
						"contextWindow": json.Number("1"),
						"maxTokens":     json.Number("1"),
						"cost": map[string]any{
							"input": json.Number("0"), "output": json.Number("0"),
							"cacheRead": json.Number("0"), "cacheWrite": json.Number("0"),
						},
						"custom": "keep-me",
					},
				},
				"extra": "keep-me",
			},
		},
	}
	return &PiConfig{
		Path:   filepath.Join(t.TempDir(), "models.json"),
		Root:   root,
		Exists: true,
	}
}

func desiredForTest() []map[string]any {
	return []map[string]any{
		{
			"id": "cn:deepseek-v4.1-flash", "name": "New Name", "reasoning": true,
			"input": []string{"text", "image"}, "contextWindow": int64(1000000), "maxTokens": int64(393216),
			"cost":             map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
			"thinkingLevelMap": map[string]any{"off": "none", "low": "low", "high": "high", "max": "max"},
		},
		{
			"id": "cn:new", "name": "New Model", "reasoning": true,
			"input": []string{"text"}, "contextWindow": int64(1000), "maxTokens": int64(100),
			"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		},
	}
}

func TestBuildPlanOverwriteAndDelete(t *testing.T) {
	cfg := testConfig(t)
	plan, err := BuildPlan(cfg, "workbuddy", "http://127.0.0.1:7863", "new-key", desiredForTest(), false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Added != 1 || plan.Updated != 1 || plan.Removed != 1 || plan.Unchanged != 0 {
		t.Fatalf("plan counts = added %d updated %d removed %d unchanged %d", plan.Added, plan.Updated, plan.Removed, plan.Unchanged)
	}

	prov := cfg.Root["providers"].(map[string]any)["workbuddy"].(map[string]any)
	models := prov["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}
	if models[0].(map[string]any)["id"] != "cn:deepseek-v4.1-flash" || models[1].(map[string]any)["id"] != "cn:new" {
		t.Errorf("model order not preserved")
	}
	updated := models[0].(map[string]any)
	if updated["custom"] != "keep-me" {
		t.Errorf("custom field not preserved: %#v", updated["custom"])
	}
	if updated["name"] != "New Name" {
		t.Errorf("name = %#v", updated["name"])
	}
	if prov["name"] != "Custom Name" || prov["extra"] != "keep-me" {
		t.Errorf("provider-level fields were overwritten")
	}
	if prov["apiKey"] != "existing-key" {
		t.Errorf("apiKey = %#v, want existing key preserved", prov["apiKey"])
	}
	other := cfg.Root["providers"].(map[string]any)["other"].(map[string]any)
	if len(other["models"].([]any)) != 1 {
		t.Errorf("other provider was modified")
	}
	if !strings.Contains(plan.Render(), "cn:old") {
		t.Errorf("render does not mention removed model")
	}
}

func TestBuildPlanKeepMissing(t *testing.T) {
	cfg := testConfig(t)
	plan, err := BuildPlan(cfg, "workbuddy", "http://127.0.0.1:7863", "new-key", desiredForTest(), true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Removed != 0 {
		t.Fatalf("removed = %d, want 0", plan.Removed)
	}
	prov := cfg.Root["providers"].(map[string]any)["workbuddy"].(map[string]any)
	models := prov["models"].([]any)
	if len(models) != 3 {
		t.Fatalf("models = %d, want 3", len(models))
	}
	if models[2].(map[string]any)["id"] != "cn:old" {
		t.Errorf("missing model not kept at the end")
	}
}

func TestEnsureProviderCreatesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	cfg, err := LoadPiConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(cfg, "workbuddy", "http://127.0.0.1:7863", "key", desiredForTest()[:1], false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Added != 1 {
		t.Fatalf("added = %d", plan.Added)
	}
	prov := cfg.Root["providers"].(map[string]any)["workbuddy"].(map[string]any)
	if prov["baseUrl"] != "http://127.0.0.1:7863/v1" {
		t.Errorf("baseUrl = %#v", prov["baseUrl"])
	}
	if prov["apiKey"] != "key" {
		t.Errorf("apiKey = %#v", prov["apiKey"])
	}
	compat := prov["compat"].(map[string]any)
	if compat["maxTokensField"] != "max_tokens" || compat["supportsReasoningEffort"] != true {
		t.Errorf("compat = %#v", compat)
	}
}

func TestAtomicWriteWithoutBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	if err := os.WriteFile(path, []byte("old-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(path, []byte("new-content\n")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new-content\n" {
		t.Fatalf("content = %q, err = %v", got, err)
	}
	backups, _ := filepath.Glob(path + ".bak.*")
	if len(backups) != 0 {
		t.Fatalf("unexpected backup files: %v", backups)
	}
	temps, _ := filepath.Glob(filepath.Join(dir, ".models.json.tmp-*"))
	if len(temps) != 0 {
		t.Fatalf("unexpected temp files: %v", temps)
	}
}
