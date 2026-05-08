package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pinealctx/kiro-gateway/core/providers"
	"github.com/pinealctx/kiro-gateway/middleware"
	kiroProvider "github.com/pinealctx/kiro-gateway/providers/kiro"
)

// KiroUsageHandler exposes Kiro account quota information to API-key clients.
type KiroUsageHandler struct {
	registry *providers.Registry
}

func NewKiroUsageHandler(registry *providers.Registry) *KiroUsageHandler {
	return &KiroUsageHandler{registry: registry}
}

// GetUsageLimits returns subscription and quota information for the resolved Kiro account.
// GET /v1/kiro/usage-limits
// GET /a/:kiro_account/v1/kiro/usage-limits
func (h *KiroUsageHandler) GetUsageLimits(c *gin.Context) {
	account, ok := middleware.ResolveKiroAccount(c, c.Param("kiro_account"))
	if !ok {
		return
	}

	entry, exists := h.registry.Entries()[account]
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"message": "Kiro account not found",
				"type":    "not_found_error",
			},
		})
		return
	}
	provider, ok := entry.Provider.(*kiroProvider.Provider)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "resolved account is not a Kiro account",
				"type":    "invalid_request_error",
			},
		})
		return
	}

	limits, err := provider.GetUsageLimits(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "upstream_error",
			},
		})
		return
	}
	c.JSON(http.StatusOK, limits)
}
