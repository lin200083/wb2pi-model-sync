package main

import (
	"encoding/json"
	"os"
	"testing"
)

func loadFixture(t *testing.T, path string, dst any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func fixtureModels(t *testing.T) []Model {
	t.Helper()
	var v1 struct {
		Data []Model `json:"data"`
	}
	loadFixture(t, "testdata/v1_models.json", &v1)
	var panel struct {
		Models []Model `json:"models"`
	}
	loadFixture(t, "testdata/panel_models.json", &panel)
	merged, _ := mergeModels(v1.Data, panel.Models)
	return merged
}

func findModel(t *testing.T, models []Model, id string) *Model {
	t.Helper()
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	t.Fatalf("model %s not found", id)
	return nil
}

func TestMappingDeepseekV41Flash(t *testing.T) {
	models := fixtureModels(t)
	m := findModel(t, models, "cn:deepseek-v4.1-flash")
	if got, want := m.DisplayName(), "Deepseek-V4.1-Flash (x0.11)"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
	}
	entry, err := ToPiModel(*m)
	if err != nil {
		t.Fatal(err)
	}
	if got := entry["maxTokens"]; got != int64(393216) {
		t.Errorf("maxTokens = %v, want 393216", got)
	}
	if got := entry["contextWindow"]; got != int64(1000000) {
		t.Errorf("contextWindow = %v, want 1000000", got)
	}
	inputs, ok := entry["input"].([]string)
	if !ok || len(inputs) != 2 || inputs[1] != "image" {
		t.Errorf("input = %#v, want text+image", entry["input"])
	}
	tm, ok := entry["thinkingLevelMap"].(map[string]any)
	if !ok {
		t.Fatal("thinkingLevelMap missing")
	}
	for level, want := range map[string]any{
		"off": "none", "low": "low", "high": "high", "max": "max",
		"minimal": nil, "medium": nil, "xhigh": nil,
	} {
		if got := tm[level]; got != want {
			t.Errorf("thinkingLevelMap[%q] = %#v, want %#v", level, got, want)
		}
	}
}

func TestMappingModelsWithoutOffOrPrice(t *testing.T) {
	models := fixtureModels(t)

	hy3 := findModel(t, models, "cn:hy3")
	if hy3.CanDisable() {
		t.Error("cn:hy3 should not allow disabling thinking")
	}
	hy3Entry, err := ToPiModel(*hy3)
	if err != nil {
		t.Fatal(err)
	}
	if got := hy3Entry["name"]; got != "Hy3 (x0.00)" {
		t.Errorf("hy3 name = %v", got)
	}
	hy3Map := hy3Entry["thinkingLevelMap"].(map[string]any)
	if hy3Map["off"] != nil {
		t.Errorf("hy3 off = %#v, want nil", hy3Map["off"])
	}
	if hy3Map["low"] != "low" || hy3Map["high"] != "high" {
		t.Errorf("hy3 efforts = %#v", hy3Map)
	}

	hy4f := findModel(t, models, "cn:hy4-preview-f")
	hy4fEntry, err := ToPiModel(*hy4f)
	if err != nil {
		t.Fatal(err)
	}
	if got := hy4fEntry["name"]; got != "Hy4 preview" {
		t.Errorf("hy4-preview-f name = %v, want no multiplier suffix", got)
	}
}

func TestMappingMediumOnlyAndLargeOutput(t *testing.T) {
	models := fixtureModels(t)
	k3 := findModel(t, models, "cn:kimi-k3-1")
	entry, err := ToPiModel(*k3)
	if err != nil {
		t.Fatal(err)
	}
	if got := entry["name"]; got != "Kimi-K3 (x1.62)" {
		t.Errorf("name = %v", got)
	}
	if got := entry["maxTokens"]; got != int64(1048576) {
		t.Errorf("maxTokens = %v, want copied upstream 1048576", got)
	}
	tm := entry["thinkingLevelMap"].(map[string]any)
	if tm["medium"] != "medium" {
		t.Errorf("medium = %#v", tm["medium"])
	}
	if tm["off"] != nil {
		t.Errorf("off = %#v, want nil", tm["off"])
	}
}

func TestMultiplierSuffix(t *testing.T) {
	cases := map[string]string{
		"x0.11":         "(x0.11)",
		"x0.71 credits": "(x0.71)",
		"x0.00":         "(x0.00)",
		"0x":            "",
		"":              "",
		"x":             "",
	}
	for in, want := range cases {
		if got := multiplierSuffix(in); got != want {
			t.Errorf("multiplierSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildDesiredExcludesAutoAndFilters(t *testing.T) {
	models := fixtureModels(t)
	desired, warnings, err := BuildDesired(models, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(desired) != 19 {
		t.Errorf("desired = %d, want 19", len(desired))
	}
	for _, m := range desired {
		if m["id"] == "cn:auto" {
			t.Error("cn:auto must be excluded")
		}
	}

	only, _, err := BuildDesired(models, map[string]bool{"cn:hy3": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0]["id"] != "cn:hy3" {
		t.Errorf("only filter result = %#v", only)
	}
}
