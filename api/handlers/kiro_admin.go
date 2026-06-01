package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pinealctx/kiro-gateway/core/providers"
	kiroProvider "github.com/pinealctx/kiro-gateway/providers/kiro"
)

// KiroAdminHandler provides Kiro PKCE login management endpoints.
type KiroAdminHandler struct {
	registry *providers.Registry
}

func NewKiroAdminHandler(registry *providers.Registry) *KiroAdminHandler {
	return &KiroAdminHandler{registry: registry}
}

// findKiroProvider finds a Kiro provider by name, or the first one if name is empty.
func (h *KiroAdminHandler) findKiroProvider(name string) *kiroProvider.Provider {
	if name != "" {
		if p, ok := h.registry.Get(name); ok {
			if kp, ok := p.(*kiroProvider.Provider); ok {
				return kp
			}
		}
		return nil
	}
	// Find first Kiro provider
	for _, p := range h.registry.All() {
		if kp, ok := p.(*kiroProvider.Provider); ok {
			return kp
		}
	}
	return nil
}

// StartLogin initiates a Kiro PKCE authorization code login flow.
// POST /admin/kiro/login?provider=<name>
// Body: { "port": 3128 } (optional, default 3128)
func (h *KiroAdminHandler) StartLogin(c *gin.Context) {
	providerName := c.Query("provider")
	provider := h.findKiroProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no kiro provider configured"})
		return
	}

	var req struct {
		Port int `json:"port"`
	}
	_ = c.ShouldBindJSON(&req) // optional body

	session, err := provider.AuthMgr().StartLogin(req.Port)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":       session.ID,
		"auth_url": session.AuthURL,
		"port":     session.CallbackPort,
		"status":   session.Status,
	})
}

// StartDeviceLogin initiates an AWS OIDC device-code login directly.
// POST /admin/kiro/device-login?provider=<name>
// Body: { "method": "organization", "idc_region": "us-east-1", "start_url": "https://your-org.awsapps.com/start" }
func (h *KiroAdminHandler) StartDeviceLogin(c *gin.Context) {
	providerName := c.Query("provider")
	provider := h.findKiroProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no kiro provider configured"})
		return
	}

	var req struct {
		Method    string `json:"method"`
		IDCRegion string `json:"idc_region"`
		StartURL  string `json:"start_url"`
	}
	_ = c.ShouldBindJSON(&req)
	req.Method = strings.ToLower(strings.TrimSpace(req.Method))
	req.IDCRegion = strings.TrimSpace(req.IDCRegion)
	req.StartURL = strings.TrimSpace(req.StartURL)

	switch req.Method {
	case "", "organization", "your_organization", "org", "awsidc":
		req.Method = "organization"
		if req.StartURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "start_url is required for organization device login"})
			return
		}
	case "builder_id", "builderid":
		req.Method = "builder_id"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported device login method"})
		return
	}

	session, err := provider.AuthMgr().StartDeviceLogin(req.IDCRegion, req.StartURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":                        session.ID,
		"user_code":                 session.UserCode,
		"verification_uri":          session.VerifyURI,
		"verification_uri_complete": session.VerifyURIComplete,
		"interval":                  session.Interval,
		"expires_at":                session.ExpiresAt,
		"status":                    session.Status,
		"method":                    req.Method,
	})
}

// GetLoginStatus checks the status of a Kiro PKCE login session.
// GET /admin/kiro/login/:id?provider=<name>
func (h *KiroAdminHandler) GetLoginStatus(c *gin.Context) {
	providerName := c.Query("provider")
	provider := h.findKiroProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no kiro provider configured"})
		return
	}

	id := c.Param("id")
	session, ok := provider.AuthMgr().GetSession(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	session.Mu().Lock()
	status := session.Status
	errStr := session.Error
	session.Mu().Unlock()

	resp := gin.H{
		"id":     id,
		"status": status,
	}
	if errStr != "" {
		resp["error"] = errStr
	}

	c.JSON(http.StatusOK, resp)
}

// CompleteLogin finalizes a Kiro PKCE login by injecting the token into the provider.
// POST /admin/kiro/login/complete/:id?provider=<name>
func (h *KiroAdminHandler) CompleteLogin(c *gin.Context) {
	providerName := c.Query("provider")
	provider := h.findKiroProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no kiro provider configured"})
		return
	}

	id := c.Param("id")
	session, ok := provider.AuthMgr().GetSession(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	session.Mu().Lock()
	status := session.Status
	accessToken := session.AccessToken
	refreshToken := session.RefreshToken
	clientID := session.ClientID
	clientSecret := session.ClientSecret
	tokenEndpoint := session.TokenEndpoint
	tokenExpiresAt := session.TokenExpiresAt
	profileArn := session.ProfileArn
	session.Mu().Unlock()

	if status != "completed" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session not completed yet", "status": status})
		return
	}

	if accessToken == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no access token available"})
		return
	}

	lt := &kiroProvider.LoginToken{
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		TokenEndpoint: tokenEndpoint,
		ExpiresAt:     tokenExpiresAt,
		IsExternalIdP: session.IsExternalIdP(),
		ProfileArn:    profileArn,
	}

	provider.SetLoginToken(lt)
	h.registry.CheckHealthFor(provider.Name())
	provider.AuthMgr().RemoveSession(id)

	c.JSON(http.StatusOK, gin.H{
		"message":  "Kiro token activated via PKCE login",
		"provider": provider.Name(),
	})
}

// GetStatus shows the current Kiro token status.
// GET /admin/kiro/status?provider=<name>
func (h *KiroAdminHandler) GetStatus(c *gin.Context) {
	providerName := c.Query("provider")
	provider := h.findKiroProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no kiro provider configured"})
		return
	}

	c.JSON(http.StatusOK, provider.TokenStatus())
}

// GetUsageLimits shows cached Kiro subscription and quota limits.
// GET /admin/kiro/usage-limits?provider=<name>&refresh=true
func (h *KiroAdminHandler) GetUsageLimits(c *gin.Context) {
	providerName := c.Query("provider")
	provider := h.findKiroProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no kiro provider configured"})
		return
	}

	var (
		limits *kiroProvider.UsageLimits
		err    error
	)
	if strings.EqualFold(c.Query("refresh"), "true") || c.Query("refresh") == "1" {
		limits, err = provider.RefreshUsageLimits(c.Request.Context())
	} else {
		limits, err = provider.GetCachedOrRefreshUsageLimits(c.Request.Context())
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, limits)
}

// GetModels lists models available to the selected Kiro account.
// GET /admin/kiro/models?provider=<name>
func (h *KiroAdminHandler) GetModels(c *gin.Context) {
	providerName := c.Query("provider")
	provider := h.findKiroProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no kiro provider configured"})
		return
	}

	models, err := provider.ListModelDetails(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"provider": provider.Name(),
		"models":   models,
		"total":    len(models),
	})
}

// RefreshToken forces a token refresh.
// POST /admin/kiro/refresh?provider=<name>
func (h *KiroAdminHandler) RefreshToken(c *gin.Context) {
	providerName := c.Query("provider")
	provider := h.findKiroProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no kiro provider configured"})
		return
	}

	if err := provider.ForceRefresh(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.registry.CheckHealthFor(provider.Name())

	c.JSON(http.StatusOK, provider.TokenStatus())
}

// ImportLocal reads the local kiro-cli SQLite database and injects the token.
// POST /admin/kiro/import-local?provider=<name>
// Body (optional): { "db_path": "/path/to/data.sqlite3" }
func (h *KiroAdminHandler) ImportLocal(c *gin.Context) {
	providerName := c.Query("provider")
	provider := h.findKiroProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no kiro provider configured"})
		return
	}

	var req struct {
		DBPath string `json:"db_path"`
	}
	_ = c.ShouldBindJSON(&req) // optional body

	lt, err := kiroProvider.ImportLocalToken(req.DBPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	provider.SetLoginToken(lt)
	h.registry.CheckHealthFor(provider.Name())

	resp := gin.H{
		"message":         "kiro-cli token imported",
		"provider":        provider.Name(),
		"is_external_idp": lt.IsExternalIdP,
		"has_refresh":     lt.RefreshToken != "",
		"profile_arn":     lt.ProfileArn,
	}
	if !lt.ExpiresAt.IsZero() {
		resp["expires_at"] = lt.ExpiresAt
	}

	c.JSON(http.StatusOK, resp)
}
