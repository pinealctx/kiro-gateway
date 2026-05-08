package handlers

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pinealctx/kiro-gateway/middleware"
	"github.com/pinealctx/kiro-gateway/tenant"
)

func recordUsage(store *tenant.Store, c *gin.Context, model, provider string, inputTokens, outputTokens int, start time.Time) {
	if store == nil {
		return
	}
	keyID := middleware.GetTenantID(c)
	if keyID == "" {
		return
	}
	total := inputTokens + outputTokens
	_ = store.RecordUsage(&tenant.UsageRecord{
		KeyID:        keyID,
		Model:        model,
		Provider:     provider,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  total,
		Duration:     float64(time.Since(start).Microseconds()) / 1000,
		CreatedAt:    time.Now().UTC(),
	})
}
