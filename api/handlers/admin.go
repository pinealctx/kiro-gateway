package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pinealctx/kiro-gateway/config"
	"github.com/pinealctx/kiro-gateway/core/providers"
	"github.com/pinealctx/kiro-gateway/notifications"
	"github.com/pinealctx/kiro-gateway/providers/kiro"
	"github.com/pinealctx/kiro-gateway/tenant"
	"go.uber.org/zap"
)

// ProviderFactory creates an AIProvider from a ProviderConfig.
type ProviderFactory func(pc config.ProviderConfig, logger *zap.Logger) (providers.AIProvider, error)

var kiroAccountNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}(-gov)?-[a-z]+-\d+$`)

// AdminHandler provides management endpoints for API keys, providers, and usage.
type AdminHandler struct {
	store    *tenant.Store
	registry *providers.Registry
	factory  ProviderFactory
	logger   *zap.Logger
	teamsCfg config.TeamsNotificationConfig
}

func NewAdminHandler(store *tenant.Store, registry *providers.Registry, factory ProviderFactory, logger *zap.Logger, teamsCfg ...config.TeamsNotificationConfig) *AdminHandler {
	cfg := config.TeamsNotificationConfig{}
	if len(teamsCfg) > 0 {
		cfg = teamsCfg[0]
	}
	return &AdminHandler{store: store, registry: registry, factory: factory, logger: logger, teamsCfg: cfg}
}

// adminError writes a standardized error response for Admin endpoints.
func adminError(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{"message": message, "type": errType},
	})
}

// ============================================================
// Teams notification settings
// ============================================================

type updateTeamsNotificationRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

func (h *AdminHandler) GetTeamsNotification(c *gin.Context) {
	c.JSON(http.StatusOK, notifications.GetRuntimeStatus(h.store, h.teamsCfg))
}

func (h *AdminHandler) UpdateTeamsNotification(c *gin.Context) {
	var req updateTeamsNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if !notifications.GetRuntimeStatus(h.store, h.teamsCfg).Configured {
		adminError(c, http.StatusBadRequest, "invalid_request_error", "teams notifications are not configured")
		return
	}
	if err := notifications.SetRuntimeEnabled(h.store, *req.Enabled); err != nil {
		adminError(c, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, notifications.GetRuntimeStatus(h.store, h.teamsCfg))
}

// ============================================================
// POST /admin/keys — Create API key
// ============================================================

type createKeyRequest struct {
	Name               string   `json:"name" binding:"required"`
	KiroAccounts       []string `json:"kiro_accounts" binding:"required"`
	KiroDefaultAccount string   `json:"kiro_default_account"`
	SuppressReasoning  *bool    `json:"suppress_reasoning"`
}

func (h *AdminHandler) CreateKey(c *gin.Context) {
	var req createKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	opts := []tenant.KeyOption{}
	accounts, ok := h.validateKiroAccounts(c, req.KiroAccounts, true)
	if !ok {
		return
	}
	opts = append(opts, tenant.WithKiroAccounts(accounts))
	defaultAccount, ok := validateKiroDefaultAccount(c, accounts, req.KiroDefaultAccount)
	if !ok {
		return
	}
	opts = append(opts, tenant.WithKiroDefaultAccount(defaultAccount))
	if req.SuppressReasoning != nil {
		opts = append(opts, tenant.WithSuppressReasoning(*req.SuppressReasoning))
	}

	key, err := h.store.CreateKey(req.Name, opts...)
	if err != nil {
		adminError(c, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	c.JSON(http.StatusCreated, key)
}

// ============================================================
// GET /admin/keys — List all keys
// ============================================================

func (h *AdminHandler) ListKeys(c *gin.Context) {
	keys := h.store.ListKeys()
	// Mask key tokens in list response
	type maskedKey struct {
		ID                 string   `json:"id"`
		KeyPrefix          string   `json:"key_prefix"`
		Name               string   `json:"name"`
		Enabled            bool     `json:"enabled"`
		KiroAccounts       []string `json:"kiro_accounts"`
		KiroDefaultAccount string   `json:"kiro_default_account"`
		SuppressReasoning  bool     `json:"suppress_reasoning"`
		CreatedAt          string   `json:"created_at"`
	}
	result := make([]maskedKey, len(keys))
	for i, k := range keys {
		prefix := k.Key
		if len(prefix) > 10 {
			prefix = prefix[:10] + "..."
		}
		result[i] = maskedKey{
			ID:                 k.ID,
			KeyPrefix:          prefix,
			Name:               k.Name,
			Enabled:            k.Enabled,
			KiroAccounts:       k.KiroAccounts,
			KiroDefaultAccount: k.KiroDefaultAccount,
			SuppressReasoning:  k.SuppressReasoning,
			CreatedAt:          k.CreatedAt.Format(time.RFC3339),
		}
	}
	c.JSON(http.StatusOK, gin.H{"keys": result, "total": len(result)})
}

// ============================================================
// GET /admin/keys/:id — Get key detail
// ============================================================

func (h *AdminHandler) GetKey(c *gin.Context) {
	id := c.Param("id")
	key, err := h.store.GetKeyByID(id)
	if err != nil {
		adminError(c, http.StatusNotFound, "not_found", "key not found")
		return
	}
	c.JSON(http.StatusOK, key)
}

// ============================================================
// PUT /admin/keys/:id — Update key
// ============================================================

type updateKeyRequest struct {
	Name               *string  `json:"name"`
	Enabled            *bool    `json:"enabled"`
	KiroAccounts       []string `json:"kiro_accounts"`
	KiroDefaultAccount *string  `json:"kiro_default_account"`
	SuppressReasoning  *bool    `json:"suppress_reasoning"`
}

func (h *AdminHandler) UpdateKey(c *gin.Context) {
	id := c.Param("id")
	var req updateKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	opts := []tenant.KeyOption{}
	if req.Name != nil {
		opts = append(opts, tenant.WithName(*req.Name))
	}
	if req.Enabled != nil {
		opts = append(opts, tenant.WithEnabled(*req.Enabled))
	}
	if req.SuppressReasoning != nil {
		opts = append(opts, tenant.WithSuppressReasoning(*req.SuppressReasoning))
	}
	if req.KiroAccounts != nil {
		accounts, ok := h.validateKiroAccounts(c, req.KiroAccounts, true)
		if !ok {
			return
		}
		opts = append(opts, tenant.WithKiroAccounts(accounts))
		defaultAccount := ""
		if req.KiroDefaultAccount != nil {
			defaultAccount = *req.KiroDefaultAccount
		}
		if defaultAccount == "" {
			defaultAccount = accounts[0]
		}
		defaultAccount, ok = validateKiroDefaultAccount(c, accounts, defaultAccount)
		if !ok {
			return
		}
		opts = append(opts, tenant.WithKiroDefaultAccount(defaultAccount))
	} else if req.KiroDefaultAccount != nil {
		key, err := h.store.GetKeyByID(id)
		if err != nil {
			adminError(c, http.StatusNotFound, "not_found", err.Error())
			return
		}
		defaultAccount, ok := validateKiroDefaultAccount(c, key.KiroAccounts, *req.KiroDefaultAccount)
		if !ok {
			return
		}
		opts = append(opts, tenant.WithKiroDefaultAccount(defaultAccount))
	}

	key, err := h.store.UpdateKey(id, opts...)
	if err != nil {
		adminError(c, http.StatusNotFound, "not_found", err.Error())
		return
	}
	c.JSON(http.StatusOK, key)
}

func (h *AdminHandler) validateKiroAccounts(c *gin.Context, raw []string, required bool) ([]string, bool) {
	seen := make(map[string]bool, len(raw))
	accounts := make([]string, 0, len(raw))
	for _, account := range raw {
		account = strings.TrimSpace(account)
		if account == "" || seen[account] {
			continue
		}
		seen[account] = true
		if _, exists := h.store.GetProviderByName(account); !exists {
			adminError(c, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("account %q does not exist", account))
			return nil, false
		}
		accounts = append(accounts, account)
	}
	if required && len(accounts) == 0 {
		adminError(c, http.StatusBadRequest, "invalid_request_error", "kiro_accounts is required")
		return nil, false
	}
	return accounts, true
}

func validateKiroDefaultAccount(c *gin.Context, accounts []string, raw string) (string, bool) {
	defaultAccount := strings.TrimSpace(raw)
	if defaultAccount == "" && len(accounts) > 0 {
		defaultAccount = accounts[0]
	}
	for _, account := range accounts {
		if account == defaultAccount {
			return defaultAccount, true
		}
	}
	adminError(c, http.StatusBadRequest, "invalid_request_error", "kiro_default_account must be in kiro_accounts")
	return "", false
}

func validKiroAccountName(name string) bool {
	return kiroAccountNamePattern.MatchString(name)
}

func normalizeKiroRegion(region string) string {
	return strings.ToLower(strings.TrimSpace(region))
}

func validAWSRegion(region string) bool {
	return awsRegionPattern.MatchString(region)
}

// ============================================================
// DELETE /admin/keys/:id — Delete key
// ============================================================

func (h *AdminHandler) DeleteKey(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.DeleteKey(id); err != nil {
		adminError(c, http.StatusNotFound, "not_found", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// ============================================================
// POST /admin/accounts — Create Kiro account
// ============================================================

type createProviderRequest struct {
	Name   string `json:"name" binding:"required"`
	Type   string `json:"type"`
	Region string `json:"region" binding:"required"`
}

func (h *AdminHandler) CreateProvider(c *gin.Context) {
	var req createProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if req.Type != "" && req.Type != "kiro" {
		adminError(c, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("unsupported account type: %q", req.Type))
		return
	}
	req.Type = "kiro"
	req.Name = strings.TrimSpace(req.Name)
	req.Region = normalizeKiroRegion(req.Region)
	if !validKiroAccountName(req.Name) {
		adminError(c, http.StatusBadRequest, "invalid_request_error", "account name must be 1-64 chars and contain only letters, numbers, dot, underscore, or hyphen; it must start with a letter or number")
		return
	}
	if !validAWSRegion(req.Region) {
		adminError(c, http.StatusBadRequest, "invalid_request_error", "region is required and must look like us-east-1")
		return
	}

	// Check name uniqueness in DB
	if _, exists := h.store.GetProviderByName(req.Name); exists {
		adminError(c, http.StatusConflict, "conflict", fmt.Sprintf("provider %q already exists", req.Name))
		return
	}

	rec, err := h.store.CreateProvider(req.Name, req.Type, tenant.WithProviderRegion(req.Region))
	if err != nil {
		adminError(c, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	// Instantiate and register in the live registry
	if err := h.activateProvider(rec); err != nil {
		// Rollback DB record on activation failure
		_ = h.store.DeleteProvider(rec.ID)
		adminError(c, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("provider config invalid: %v", err))
		return
	}

	h.logger.Info("Provider created via admin API",
		zap.String("name", rec.Name), zap.String("type", rec.Type))

	c.JSON(http.StatusCreated, rec)
}

// ============================================================
// GET /admin/accounts — List Kiro accounts (DB + runtime status)
// ============================================================

func (h *AdminHandler) ListProviders(c *gin.Context) {
	records := h.store.ListProviderRecords()

	type providerInfo struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		Type        string            `json:"type"`
		Region      string            `json:"region"`
		Enabled     bool              `json:"enabled"`
		Healthy     bool              `json:"healthy"`
		CreatedAt   string            `json:"created_at"`
		TokenInfo   map[string]any    `json:"token_info,omitempty"`
		UsageLimits *kiro.UsageLimits `json:"usage_limits,omitempty"`
	}

	// Collect live providers for token info lookup
	runtimeAll := h.registry.All()

	result := make([]providerInfo, 0, len(records))
	for _, r := range records {
		info := providerInfo{
			ID:        r.ID,
			Name:      r.Name,
			Type:      r.Type,
			Region:    r.Region,
			Enabled:   r.Enabled,
			Healthy:   h.registry.IsHealthy(r.Name),
			CreatedAt: r.CreatedAt.Format(time.RFC3339),
		}
		// Enrich with token/auth info from live provider
		if p, ok := runtimeAll[r.Name]; ok {
			if tip, ok := p.(providers.TokenInfoProvider); ok {
				info.TokenInfo = tip.GetTokenInfo()
			}
			if kp, ok := p.(*kiro.Provider); ok {
				if limits, ok := kp.GetCachedUsageLimits(); ok {
					info.UsageLimits = limits
				}
			}
		}
		result = append(result, info)
	}

	// Also include runtime-only providers (registered via config but not in DB)
	dbNames := make(map[string]bool, len(records))
	for _, r := range records {
		dbNames[r.Name] = true
	}
	for name, p := range runtimeAll {
		if !dbNames[name] {
			info := providerInfo{
				Name:    name,
				Healthy: h.registry.IsHealthy(name),
			}
			if tip, ok := p.(providers.TokenInfoProvider); ok {
				info.TokenInfo = tip.GetTokenInfo()
			}
			if kp, ok := p.(*kiro.Provider); ok {
				if limits, ok := kp.GetCachedUsageLimits(); ok {
					info.UsageLimits = limits
				}
			}
			result = append(result, info)
		}
	}

	// Sort by name for stable ordering
	slices.SortFunc(result, func(a, b providerInfo) int {
		if a.Name < b.Name {
			return -1
		} else if a.Name > b.Name {
			return 1
		}
		return 0
	})

	c.JSON(http.StatusOK, gin.H{"accounts": result, "total": len(result)})
}

// ============================================================
// GET /admin/accounts/:id — Get Kiro account detail
// ============================================================

func (h *AdminHandler) GetProvider(c *gin.Context) {
	id := c.Param("id")
	rec, err := h.store.GetProvider(id)
	if err != nil {
		adminError(c, http.StatusNotFound, "not_found", "provider not found")
		return
	}
	c.JSON(http.StatusOK, rec)
}

// ============================================================
// PUT /admin/accounts/:id — Update Kiro account
// ============================================================

type updateProviderRequest struct {
	Name    *string `json:"name"`
	Type    *string `json:"type"`
	Region  *string `json:"region"`
	Enabled *bool   `json:"enabled"`
}

func (h *AdminHandler) UpdateProvider(c *gin.Context) {
	id := c.Param("id")

	var req updateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	// Get old record for name change / re-registration
	oldRec, err := h.store.GetProvider(id)
	if err != nil {
		adminError(c, http.StatusNotFound, "not_found", "provider not found")
		return
	}
	oldName := oldRec.Name

	opts := []tenant.ProviderOption{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if !validKiroAccountName(name) {
			adminError(c, http.StatusBadRequest, "invalid_request_error", "account name must be 1-64 chars and contain only letters, numbers, dot, underscore, or hyphen; it must start with a letter or number")
			return
		}
		opts = append(opts, tenant.WithProviderName(name))
	}
	if req.Type != nil {
		if *req.Type != "" && *req.Type != "kiro" {
			adminError(c, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("unsupported account type: %q", *req.Type))
			return
		}
		opts = append(opts, tenant.WithProviderType("kiro"))
	}
	if req.Region != nil {
		region := normalizeKiroRegion(*req.Region)
		if !validAWSRegion(region) {
			adminError(c, http.StatusBadRequest, "invalid_request_error", "region is required and must look like us-east-1")
			return
		}
		opts = append(opts, tenant.WithProviderRegion(region))
	}
	if req.Enabled != nil {
		opts = append(opts, tenant.WithProviderEnabled(*req.Enabled))
	}
	rec, err := h.store.UpdateProvider(id, opts...)
	if err != nil {
		adminError(c, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	// Migrate Kiro token KV data if provider name changed
	if rec.Name != oldName {
		for _, suffix := range []string{"token", "usage_limits"} {
			oldKVKey := "kiro:" + oldName + ":" + suffix
			newKVKey := "kiro:" + rec.Name + ":" + suffix
			if data, ok := h.store.GetKV(oldKVKey); ok {
				if err := h.store.SetKV(newKVKey, data); err == nil {
					_ = h.store.DeleteKV(oldKVKey)
					h.logger.Info("Migrated Kiro KV data",
						zap.String("old_key", oldKVKey),
						zap.String("new_key", newKVKey))
				}
			}
		}
	}

	// Re-register: unregister old name, register new instance
	h.registry.Unregister(oldName)
	if rec.Enabled {
		if err := h.activateProvider(rec); err != nil {
			h.logger.Error("Failed to re-activate provider after update", zap.String("name", rec.Name), zap.Error(err))
		}
	}

	h.logger.Info("Provider updated via admin API",
		zap.String("name", rec.Name), zap.String("type", rec.Type))

	c.JSON(http.StatusOK, rec)
}

// ============================================================
// DELETE /admin/accounts/:id — Delete Kiro account
// ============================================================

func (h *AdminHandler) DeleteProvider(c *gin.Context) {
	id := c.Param("id")

	rec, err := h.store.GetProvider(id)
	if err != nil {
		adminError(c, http.StatusNotFound, "not_found", "provider not found")
		return
	}

	h.registry.Unregister(rec.Name)
	if err := h.store.DeleteProvider(id); err != nil {
		adminError(c, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	// Clean up associated token/auth/cache data from kv_store
	for _, suffix := range []string{"token", "usage_limits"} {
		kvKey := "kiro:" + rec.Name + ":" + suffix
		if err := h.store.DeleteKV(kvKey); err != nil {
			h.logger.Warn("Failed to clean up Kiro KV data", zap.String("key", kvKey), zap.Error(err))
		}
	}

	h.logger.Info("Provider deleted via admin API", zap.String("name", rec.Name))
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// activateProvider converts a ProviderRecord to an AIProvider and registers it.
func (h *AdminHandler) activateProvider(rec *tenant.ProviderRecord) error {
	if rec.Type != "" && rec.Type != "kiro" {
		return fmt.Errorf("unsupported account type: %q", rec.Type)
	}
	pc := config.ProviderConfig{
		Name:    rec.Name,
		Type:    rec.Type,
		Region:  rec.Region,
		Enabled: rec.Enabled,
	}
	p, err := h.factory(pc, h.logger)
	if err != nil {
		return err
	}

	// Inject store and restore persisted tokens for Kiro providers
	if kp, ok := p.(*kiro.Provider); ok {
		kp.SetStore(h.store)
		if kp.RestoreToken() {
			h.logger.Info("Provider token restored from persistent storage", zap.String("name", rec.Name))
		}
	}

	h.registry.Register(p)

	// Schedule a deferred health check so fresh Kiro login/import state is reflected.
	name := rec.Name
	go func() {
		time.Sleep(5 * time.Second)
		h.registry.CheckHealthFor(name)
	}()

	return nil
}

// ============================================================
// GET /admin/usage — Usage statistics
// ============================================================

func (h *AdminHandler) GetUsage(c *gin.Context) {
	q := tenant.UsageQuery{
		KeyID:    c.Query("key_id"),
		Model:    c.Query("model"),
		Provider: c.Query("provider"),
		GroupBy:  c.Query("group_by"),
	}
	if from := c.Query("from"); from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err == nil {
			q.From = t
		}
	}
	if to := c.Query("to"); to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err == nil {
			q.To = t
		}
	}

	summaries, err := h.store.QueryUsage(q)
	if err != nil {
		adminError(c, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	// Support CSV export via Accept header
	if c.GetHeader("Accept") == "text/csv" {
		c.Header("Content-Type", "text/csv")
		c.Header("Content-Disposition", "attachment; filename=usage.csv")
		w := csv.NewWriter(c.Writer)
		if err := w.Write([]string{"key_id", "key_name", "model", "provider", "total_requests", "input_tokens", "output_tokens", "total_tokens"}); err != nil {
			return
		}
		for _, s := range summaries {
			if err := w.Write([]string{
				s.KeyID, s.KeyName, s.Model, s.Provider,
				fmt.Sprintf("%d", s.TotalRequests),
				fmt.Sprintf("%d", s.InputTokens),
				fmt.Sprintf("%d", s.OutputTokens),
				fmt.Sprintf("%d", s.TotalTokens),
			}); err != nil {
				return
			}
		}
		w.Flush()
		return
	}

	c.JSON(http.StatusOK, gin.H{"usage": summaries})
}
