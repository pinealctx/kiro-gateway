package handlers

import (
	"strings"

	"github.com/pinealctx/kiro-gateway/core/providers"
)

func resolveKiroAccount(registry *providers.Registry, model, accountHint string) (providers.AIProvider, string, bool) {
	model = normalizeGatewayModelID(model)
	if accountHint != "" {
		entries := registry.Entries()
		if entry, ok := entries[accountHint]; ok && registry.IsHealthy(accountHint) {
			return entry.Provider, model, true
		}
		return nil, "", false
	}
	provider, ok := registry.Resolve(model)
	if !ok {
		return nil, "", false
	}
	return provider, model, true
}

func normalizeGatewayModelID(model string) string {
	model = strings.TrimSpace(model)
	if strings.HasPrefix(model, "anthropic.") {
		return strings.TrimPrefix(model, "anthropic.")
	}
	return model
}

func claudeCodeModelID(model string) string {
	model = strings.TrimSpace(model)
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "claude") || strings.HasPrefix(lower, "anthropic") {
		return model
	}
	return "anthropic." + model
}
