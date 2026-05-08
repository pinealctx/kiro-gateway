package thinking

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	DefaultBudgetTokens = 20000
	MaxBudgetTokens     = 24576
	DefaultEffort       = "high"
)

type Config struct {
	Enabled bool
	Type    string
	Budget  int
	Effort  string
}

// ParseConfig extracts Kiro thinking configuration from OpenAI-compatible
// extra fields. It supports Anthropic-style "thinking" plus OpenAI-style
// "reasoning_effort".
func ParseConfig(extras map[string]json.RawMessage) Config {
	if len(extras) == 0 {
		return Config{}
	}

	if raw, ok := extras["thinking"]; ok && len(raw) > 0 {
		var asBool bool
		if err := json.Unmarshal(raw, &asBool); err == nil {
			if asBool {
				return Config{Enabled: true, Type: "enabled", Budget: DefaultBudgetTokens, Effort: parseEffort(extras)}
			}
			return Config{}
		}

		var asString string
		if err := json.Unmarshal(raw, &asString); err == nil {
			typ := strings.ToLower(strings.TrimSpace(asString))
			if typ == "enabled" || typ == "adaptive" {
				return Config{Enabled: true, Type: typ, Budget: DefaultBudgetTokens, Effort: parseEffort(extras)}
			}
			return Config{}
		}

		var asMap map[string]any
		if err := json.Unmarshal(raw, &asMap); err == nil {
			typ, _ := asMap["type"].(string)
			typ = strings.ToLower(strings.TrimSpace(typ))
			if typ != "enabled" && typ != "adaptive" {
				if budget, ok := toInt(asMap["budget_tokens"]); ok && budget > 0 {
					typ = "enabled"
				} else {
					return Config{}
				}
			}

			budget := DefaultBudgetTokens
			if b, ok := toInt(asMap["budget_tokens"]); ok && b > 0 {
				budget = b
				if budget > MaxBudgetTokens {
					budget = MaxBudgetTokens
				}
			}
			return Config{Enabled: true, Type: typ, Budget: budget, Effort: parseEffort(extras)}
		}
	}

	if raw, ok := extras["reasoning_effort"]; ok && len(raw) > 0 {
		var effort string
		if err := json.Unmarshal(raw, &effort); err == nil && effort != "" {
			effort = normalizeEffort(effort)
			return Config{Enabled: true, Type: "adaptive", Budget: DefaultBudgetTokens, Effort: effort}
		}
	}

	return Config{}
}

func GenerateHint(cfg Config) string {
	if !cfg.Enabled {
		return ""
	}
	if cfg.Type == "adaptive" {
		return fmt.Sprintf("<thinking_mode>adaptive</thinking_mode><thinking_effort>%s</thinking_effort>", cfg.Effort)
	}
	return fmt.Sprintf("<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>", cfg.Budget)
}

func InjectHint(systemPrompt string, cfg Config) string {
	if !cfg.Enabled || strings.Contains(systemPrompt, "<thinking_mode>") {
		return systemPrompt
	}
	hint := GenerateHint(cfg)
	if strings.TrimSpace(systemPrompt) == "" {
		return hint
	}
	return hint + "\n\n" + systemPrompt
}

func parseEffort(extras map[string]json.RawMessage) string {
	for _, key := range []string{"output_config", "outputConfig"} {
		raw, ok := extras[key]
		if !ok {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if effort, ok := m["effort"].(string); ok {
			return normalizeEffort(effort)
		}
	}
	return DefaultEffort
}

func normalizeEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return DefaultEffort
	}
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}
