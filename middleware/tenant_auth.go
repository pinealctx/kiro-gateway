package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pinealctx/kiro-gateway/tenant"
)

const (
	// Context keys for downstream handlers
	CtxKeyTenantID    = "tenant_id"
	CtxKeyTenantName  = "tenant_name"
	CtxKeyAPIKey      = "api_key"
	CtxKeyKiroAccount = "kiro_account"
)

// TenantAuth authenticates requests against the API key store.
// API keys are used for Kiro account routing and access control only.
func TenantAuth(store *tenant.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "Missing API key",
					"type":    "authentication_error",
				},
			})
			return
		}

		key, ok := store.GetKeyByToken(token)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "Invalid API key",
					"type":    "authentication_error",
				},
			})
			return
		}

		if !key.Enabled {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{
					"message": "API key is disabled",
					"type":    "permission_error",
				},
			})
			return
		}

		// Set tenant context for downstream
		c.Set(CtxKeyTenantID, key.ID)
		c.Set(CtxKeyTenantName, key.Name)
		c.Set(CtxKeyAPIKey, key)
		c.Next()
	}
}

// CheckModelPermission intentionally does not enforce model restrictions.
// Kiro account/API key routing is identity-only; model choice is supplied by the client.
func CheckModelPermission(c *gin.Context, model string) bool {
	return true
}

// GetTenantID extracts tenant ID from gin context (returns "" if no tenant auth).
func GetTenantID(c *gin.Context) string {
	id, _ := c.Get(CtxKeyTenantID)
	if id == nil {
		return ""
	}
	return id.(string)
}

// SuppressReasoning reports whether the authenticated API key is configured to
// drop upstream reasoning/thinking content from responses. Returns false when
// there is no tenant auth context.
func SuppressReasoning(c *gin.Context) bool {
	keyVal, exists := c.Get(CtxKeyAPIKey)
	if !exists {
		return false
	}
	key, ok := keyVal.(*tenant.APIKey)
	if !ok {
		return false
	}
	return key.SuppressReasoning
}

// ResolveKiroAccount verifies the URL-selected account against the API key's
// allowed Kiro account list.
func ResolveKiroAccount(c *gin.Context, requestedAccount string) (string, bool) {
	keyVal, exists := c.Get(CtxKeyAPIKey)
	if !exists {
		return strings.TrimSpace(requestedAccount), true
	}
	key := keyVal.(*tenant.APIKey)
	if len(key.KiroAccounts) == 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": "API key has no Kiro accounts assigned",
				"type":    "permission_error",
			},
		})
		return "", false
	}

	requestedAccount = strings.TrimSpace(requestedAccount)
	if requestedAccount == "" {
		requestedAccount = strings.TrimSpace(key.KiroDefaultAccount)
		if requestedAccount == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{
					"message": "API key has no default Kiro account assigned",
					"type":    "permission_error",
				},
			})
			return "", false
		}
	}
	for _, account := range key.KiroAccounts {
		if account == requestedAccount {
			c.Set(CtxKeyKiroAccount, account)
			return account, true
		}
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error": gin.H{
			"message": fmt.Sprintf("Kiro account %q not allowed for this API key", requestedAccount),
			"type":    "permission_error",
		},
	})
	return "", false
}

// ExtractBearerToken extracts the Bearer token from Authorization or x-api-key headers.
func ExtractBearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token != "" && token != auth {
		return token
	}
	// Anthropic SDK uses x-api-key
	return c.GetHeader("x-api-key")
}

func extractToken(c *gin.Context) string {
	return ExtractBearerToken(c)
}
