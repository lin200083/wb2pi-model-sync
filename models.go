package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// FlexString accepts JSON strings, numbers, or null and stores the textual form.
type FlexString string

func (s *FlexString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*s = ""
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = FlexString(str)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(b, &num); err == nil {
		*s = FlexString(num.String())
		return nil
	}
	return fmt.Errorf("expected string, number or null, got %s", string(b))
}

func (s FlexString) String() string { return string(s) }

// Model is the union of /v1/models and /panel/api/models fields.
type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	ContextLength   int64 `json:"context_length"`
	MaxOutputTokens int64 `json:"max_output_tokens"`

	SupportsReasoning bool `json:"supports_reasoning"`
	SupportsImages    bool `json:"supports_images"`
	SupportsToolCall  bool `json:"supports_tool_call"`
	OnlyReasoning     bool `json:"only_reasoning"`

	CanDisableThinking *bool      `json:"can_disable_thinking"`
	Credits            FlexString `json:"credits"`
	Efforts            []string   `json:"reasoning_supported_efforts"`
	DefaultEffort      FlexString `json:"reasoning_default_effort"`
	IsDefault          bool       `json:"is_default"`

	Tags   []string `json:"tags"`
	Vendor string   `json:"vendor"`

	// Panel-only fields.
	PanelEfforts       []string   `json:"supported_efforts"`
	PanelDefaultEffort FlexString `json:"default_effort"`
	PromoCredits       FlexString `json:"promo_credits"`
	PromoFactor        *float64   `json:"promo_factor"`
	PromoLabel         string     `json:"promo_label"`
	PromoNote          string     `json:"promo_note"`
}

func (m *Model) MergePanel(p *Model) {
	if p == nil {
		return
	}
	if p.CanDisableThinking != nil {
		m.CanDisableThinking = p.CanDisableThinking
	}
	if len(p.PanelEfforts) > 0 {
		m.PanelEfforts = append([]string(nil), p.PanelEfforts...)
	}
	if p.PanelDefaultEffort != "" {
		m.PanelDefaultEffort = p.PanelDefaultEffort
	}
	if p.PromoLabel != "" {
		m.PromoLabel = p.PromoLabel
	}
	if p.PromoNote != "" {
		m.PromoNote = p.PromoNote
	}
	if p.PromoCredits != "" {
		m.PromoCredits = p.PromoCredits
	}
	if p.PromoFactor != nil {
		m.PromoFactor = p.PromoFactor
	}
	if p.IsDefault {
		m.IsDefault = true
	}
	if m.Name == "" {
		m.Name = p.Name
	}
	if m.Description == "" {
		m.Description = p.Description
	}
	if m.ContextLength == 0 {
		m.ContextLength = p.ContextLength
	}
	if m.MaxOutputTokens == 0 {
		m.MaxOutputTokens = p.MaxOutputTokens
	}
	if m.Credits == "" {
		m.Credits = p.Credits
	}
	if m.DefaultEffort == "" {
		m.DefaultEffort = p.DefaultEffort
	}
	if len(m.Efforts) == 0 {
		m.Efforts = append([]string(nil), p.Efforts...)
	}
	if m.Vendor == "" {
		m.Vendor = p.Vendor
	}
	if len(m.Tags) == 0 {
		m.Tags = append([]string(nil), p.Tags...)
	}
	m.SupportsReasoning = m.SupportsReasoning || p.SupportsReasoning
	m.SupportsImages = m.SupportsImages || p.SupportsImages
	m.SupportsToolCall = m.SupportsToolCall || p.SupportsToolCall
	m.OnlyReasoning = m.OnlyReasoning || p.OnlyReasoning
}

func (m *Model) EffectiveEfforts() []string {
	if len(m.PanelEfforts) > 0 {
		return m.PanelEfforts
	}
	return m.Efforts
}

func (m *Model) EffectiveDefaultEffort() string {
	if m.PanelDefaultEffort != "" {
		return m.PanelDefaultEffort.String()
	}
	return m.DefaultEffort.String()
}

func (m *Model) CanDisable() bool {
	return m.CanDisableThinking != nil && *m.CanDisableThinking
}

var (
	creditMultiplierRe = regexp.MustCompile(`(?i)x\s*([0-9]+(?:\.[0-9]+)?)`)
	creditSuffixRe     = regexp.MustCompile(`(?i)\s*\(x\s*[0-9]+(?:\.[0-9]+)?(?:\s*credits?)?\)\s*$`)
)

func multiplierSuffix(credits string) string {
	m := creditMultiplierRe.FindStringSubmatch(credits)
	if len(m) != 2 {
		return ""
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("(x%.2f)", f)
}

func stripCreditSuffix(name string) string {
	return strings.TrimSpace(creditSuffixRe.ReplaceAllString(name, ""))
}

func (m *Model) DisplayName() string {
	name := stripCreditSuffix(strings.TrimSpace(m.Name))
	if name == "" {
		name = strings.TrimPrefix(m.ID, "cn:")
	}
	if suffix := multiplierSuffix(m.Credits.String()); suffix != "" {
		name += " " + suffix
	}
	return name
}

var piThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

func BuildThinkingLevelMap(m *Model) map[string]any {
	efforts := map[string]bool{}
	for _, e := range m.EffectiveEfforts() {
		efforts[strings.ToLower(strings.TrimSpace(e))] = true
	}
	out := make(map[string]any, len(piThinkingLevels))
	for _, level := range piThinkingLevels {
		if level == "off" {
			if m.CanDisable() {
				out["off"] = "none"
			} else {
				out["off"] = nil
			}
			continue
		}
		if efforts[level] {
			out[level] = level
		} else {
			out[level] = nil
		}
	}
	// Some models (e.g. auto) don't advertise a tier list. Keep the default tier
	// usable instead of hiding every non-off level.
	if len(efforts) == 0 {
		def := strings.ToLower(strings.TrimSpace(m.EffectiveDefaultEffort()))
		for _, level := range piThinkingLevels {
			if level != "off" && level == def {
				out[level] = level
				break
			}
		}
	}
	return out
}

func FormatEfforts(m map[string]any) string {
	if len(m) == 0 {
		return "—"
	}
	var parts []string
	for _, level := range piThinkingLevels {
		v, ok := m[level]
		if !ok || v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if level == "off" {
			if s == "none" {
				parts = append(parts, "off")
			}
			continue
		}
		parts = append(parts, level)
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, "/")
}

func ToPiModel(m Model) (map[string]any, error) {
	if strings.TrimSpace(m.ID) == "" {
		return nil, fmt.Errorf("模型缺少 id")
	}
	if m.ContextLength <= 0 {
		return nil, fmt.Errorf("%s 缺少 context_length", m.ID)
	}
	maxOut := m.MaxOutputTokens
	if maxOut <= 0 {
		maxOut = min(m.ContextLength, 32768)
	}

	inputs := []string{"text"}
	if m.SupportsImages {
		inputs = append(inputs, "image")
	}

	entry := map[string]any{
		"id":            m.ID,
		"name":          m.DisplayName(),
		"reasoning":     m.SupportsReasoning,
		"input":         inputs,
		"contextWindow": m.ContextLength,
		"maxTokens":     maxOut,
		"cost": map[string]any{
			"input":      0,
			"output":     0,
			"cacheRead":  0,
			"cacheWrite": 0,
		},
	}
	if m.SupportsReasoning {
		entry["thinkingLevelMap"] = BuildThinkingLevelMap(&m)
	}
	return entry, nil
}
