package routes

import (
	"crypto/hmac"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pinealctx/kiro-gateway/api/handlers"
	"github.com/pinealctx/kiro-gateway/config"
	"github.com/pinealctx/kiro-gateway/core/providers"
	"github.com/pinealctx/kiro-gateway/middleware"
	"github.com/pinealctx/kiro-gateway/tenant"
	"github.com/pinealctx/kiro-gateway/version"
	"github.com/pinealctx/kiro-gateway/web"
	"go.uber.org/zap"
)

// RouterConfig holds everything needed to set up routes.
type RouterConfig struct {
	Registry        *providers.Registry
	Logger          *zap.Logger
	AdminKey        string                   // Admin API authentication key
	AdminLocalOnly  bool                     // Restrict Admin API to loopback clients
	Store           *tenant.Store            // Always set — used for provider/key storage
	CORSOrigins     []string                 // Allowed CORS origins (empty = allow all)
	ProviderFactory handlers.ProviderFactory // Factory for dynamic provider management
	Notifications   config.NotificationsConfig
}

func SetupRouter(cfg RouterConfig) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Global middleware
	r.Use(gin.CustomRecovery(func(c *gin.Context, recovered any) {
		reqID, _ := c.Get("request_id")
		if reqID == nil {
			reqID = ""
		}
		cfg.Logger.Error("panic recovered",
			zap.Any("panic", recovered),
			zap.String("request_id", reqID.(string)),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("client_ip", c.ClientIP()),
		)
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(cfg.Logger))
	r.Use(middleware.CORS(cfg.CORSOrigins))

	// Root endpoint: service info
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"name":    "Kiro Gateway",
			"version": version.Get(),
			"endpoints": []string{
				"/v1/chat/completions",
				"/v1/models",
				"/v1/messages",
				"/v1/messages/count_tokens",
				"/v1/kiro/usage-limits",
				"/a/{kiro_account}/v1/chat/completions",
				"/a/{kiro_account}/v1/models",
				"/a/{kiro_account}/v1/messages",
				"/a/{kiro_account}/v1/messages/count_tokens",
				"/a/{kiro_account}/v1/kiro/usage-limits",
				"/health",
				"/admin/keys",
				"/admin/accounts",
				"/admin/usage",
				"/admin/kiro/login",
				"/admin/kiro/device-login",
				"/admin/kiro/status",
				"/admin/kiro/usage-limits",
				"/admin/kiro/models",
				"/ui",
			},
		})
	})

	// Health check (no auth)
	r.GET("/health", func(c *gin.Context) {
		status := gin.H{"status": "ok"}
		status["version"] = version.Get()
		for name, p := range cfg.Registry.All() {
			status[name] = p.IsHealthy(c.Request.Context())
		}
		c.JSON(http.StatusOK, status)
	})

	// Web admin UI (no auth — SPA handles admin key via localStorage)
	// Handle both /ui and /ui/* with the same handler for SPA routing
	uiHandler := gin.WrapH(http.StripPrefix("/ui", web.Handler()))
	r.GET("/ui", uiHandler)
	r.GET("/ui/*filepath", uiHandler)

	openaiH := handlers.NewOpenAIHandler(cfg.Registry, cfg.Store, cfg.Logger)
	anthropicH := handlers.NewAnthropicHandler(cfg.Registry, cfg.Store, cfg.Logger)
	kiroUsageH := handlers.NewKiroUsageHandler(cfg.Registry)
	registerRuntimeRoutes := func(group *gin.RouterGroup) {
		// OpenAI-compatible endpoints
		group.POST("/v1/chat/completions", openaiH.ChatCompletions)
		group.GET("/v1/models", openaiH.Models)

		// Anthropic-compatible endpoints
		group.POST("/v1/messages", anthropicH.Messages)
		group.POST("/v1/messages/count_tokens", anthropicH.CountTokens)

		// Kiro account quota endpoint, authenticated by the same API key.
		group.GET("/v1/kiro/usage-limits", kiroUsageH.GetUsageLimits)
	}

	api := r.Group("/")
	api.Use(middleware.TenantAuth(cfg.Store))
	registerRuntimeRoutes(api)

	accountAPI := r.Group("/a/:kiro_account")
	accountAPI.Use(middleware.TenantAuth(cfg.Store))
	registerRuntimeRoutes(accountAPI)

	// Admin API (separate auth with admin key)
	if cfg.Store != nil {
		admin := r.Group("/admin")
		if cfg.AdminLocalOnly {
			admin.Use(localOnlyAdmin())
		}
		admin.Use(adminAuth(cfg.AdminKey))

		adminH := handlers.NewAdminHandler(cfg.Store, cfg.Registry, cfg.ProviderFactory, cfg.Logger, cfg.Notifications.Teams)
		admin.POST("/keys", adminH.CreateKey)
		admin.GET("/keys", adminH.ListKeys)
		admin.GET("/keys/:id", adminH.GetKey)
		admin.PUT("/keys/:id", adminH.UpdateKey)
		admin.DELETE("/keys/:id", adminH.DeleteKey)

		admin.GET("/accounts", adminH.ListProviders)
		admin.POST("/accounts", adminH.CreateProvider)
		admin.GET("/accounts/:id", adminH.GetProvider)
		admin.PUT("/accounts/:id", adminH.UpdateProvider)
		admin.DELETE("/accounts/:id", adminH.DeleteProvider)

		admin.GET("/usage", adminH.GetUsage)
		admin.GET("/notifications/teams", adminH.GetTeamsNotification)
		admin.PUT("/notifications/teams", adminH.UpdateTeamsNotification)

		// Kiro PKCE login management (dynamic provider lookup)
		kiroH := handlers.NewKiroAdminHandler(cfg.Registry)
		admin.POST("/kiro/login", kiroH.StartLogin)
		admin.POST("/kiro/device-login", kiroH.StartDeviceLogin)
		admin.GET("/kiro/login/:id", kiroH.GetLoginStatus)
		admin.POST("/kiro/login/complete/:id", kiroH.CompleteLogin)
		admin.GET("/kiro/status", kiroH.GetStatus)
		admin.GET("/kiro/usage-limits", kiroH.GetUsageLimits)
		admin.GET("/kiro/models", kiroH.GetModels)
		admin.POST("/kiro/refresh", kiroH.RefreshToken)
		admin.POST("/kiro/import-local", kiroH.ImportLocal)
	}

	return r
}

func localOnlyAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := net.ParseIP(c.ClientIP())
		if ip == nil || !ip.IsLoopback() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"message": "Admin API is restricted to localhost", "type": "permission_error"},
			})
			return
		}
		c.Next()
	}
}

// adminAuth is a simple bearer token auth for admin endpoints.
func adminAuth(adminKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if adminKey == "" {
			c.Next() // No admin key configured = no admin auth
			return
		}
		token := middleware.ExtractBearerToken(c)
		if token == "" || !hmac.Equal([]byte(token), []byte(adminKey)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "Invalid admin key", "type": "authentication_error"},
			})
			return
		}
		c.Next()
	}
}
