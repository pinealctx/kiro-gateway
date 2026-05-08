package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	kiroRestTarget   = "AmazonCodeWhispererService.GetUsageLimits"
	kiroModelsTarget = "AmazonCodeWhispererService.ListAvailableModels"
)

// UsageLimits holds Kiro subscription and quota information.
type UsageLimits struct {
	Account           string     `json:"account,omitempty"`
	Email             string     `json:"email,omitempty"`
	Tier              string     `json:"tier,omitempty"`
	RawSubscription   string     `json:"raw_subscription,omitempty"`
	SubscriptionName  string     `json:"subscription_name,omitempty"`
	SubscriptionTitle string     `json:"subscription_title,omitempty"`
	SubscriptionType  string     `json:"subscription_type,omitempty"`
	SubscriptionState string     `json:"subscription_state,omitempty"`
	ProfileArn        string     `json:"profile_arn,omitempty"`
	DaysUntilReset    *int       `json:"days_until_reset,omitempty"`
	NextDateReset     *int64     `json:"next_date_reset,omitempty"`
	Usage             UsageQuota `json:"usage"`
	FetchedAt         time.Time  `json:"fetched_at"`
}

type UsageQuota struct {
	ResourceType     string  `json:"resource_type,omitempty"`
	DisplayName      string  `json:"display_name,omitempty"`
	Used             float64 `json:"used"`
	Limit            float64 `json:"limit"`
	UsedPrecise      float64 `json:"used_precise"`
	LimitPrecise     float64 `json:"limit_precise"`
	Remaining        float64 `json:"remaining"`
	RemainingPrecise float64 `json:"remaining_precise"`
	PercentUsed      float64 `json:"percent_used"`
	OverageRate      float64 `json:"overage_rate"`
	OverageCap       float64 `json:"overage_cap"`
	Overages         float64 `json:"overages"`
	Currency         string  `json:"currency,omitempty"`
}

type ModelInfo struct {
	ModelID             string       `json:"model_id"`
	ModelName           string       `json:"model_name,omitempty"`
	Description         string       `json:"description,omitempty"`
	RateMultiplier      float64      `json:"rate_multiplier"`
	RateUnit            string       `json:"rate_unit,omitempty"`
	SupportedInputTypes []string     `json:"supported_input_types,omitempty"`
	TokenLimits         *TokenLimits `json:"token_limits,omitempty"`
	PromptCaching       *PromptCache `json:"prompt_caching,omitempty"`
	IsDefault           bool         `json:"is_default"`
}

type TokenLimits struct {
	MaxInputTokens  int `json:"max_input_tokens"`
	MaxOutputTokens int `json:"max_output_tokens"`
}

type PromptCache struct {
	SupportsPromptCaching bool `json:"supports_prompt_caching"`
}

type usageLimitsResponse struct {
	DaysUntilReset     *int              `json:"daysUntilReset"`
	NextDateReset      *float64          `json:"nextDateReset"`
	UsageBreakdownList []usageBreakdown  `json:"usageBreakdownList"`
	SubscriptionInfo   *subscriptionInfo `json:"subscriptionInfo"`
	UserInfo           *usageUserInfo    `json:"userInfo"`
}

type usageBreakdown struct {
	ResourceType              string  `json:"resourceType"`
	CurrentUsage              float64 `json:"currentUsage"`
	CurrentUsageWithPrecision float64 `json:"currentUsageWithPrecision"`
	UsageLimit                float64 `json:"usageLimit"`
	UsageLimitWithPrecision   float64 `json:"usageLimitWithPrecision"`
	CurrentOverages           float64 `json:"currentOverages"`
	OverageRate               float64 `json:"overageRate"`
	OverageCap                float64 `json:"overageCap"`
	Currency                  string  `json:"currency"`
	DisplayName               string  `json:"displayName"`
	DisplayNamePlural         string  `json:"displayNamePlural"`
}

type subscriptionInfo struct {
	SubscriptionName  string `json:"subscriptionName"`
	SubscriptionTitle string `json:"subscriptionTitle"`
	SubscriptionType  string `json:"subscriptionType"`
	Type              string `json:"type"`
	Status            string `json:"status"`
}

type usageUserInfo struct {
	Email  string `json:"email"`
	UserID string `json:"userId"`
}

// GetUsageLimits fetches the current Kiro subscription and quota limits.
func (p *Provider) GetUsageLimits(ctx context.Context) (*UsageLimits, error) {
	token, err := p.tokenMgr.GetToken()
	if err != nil {
		return nil, fmt.Errorf("token error: %w", err)
	}
	return p.client.GetUsageLimits(ctx, p.name, p.profileArn, token)
}

// ListModels fetches models available to this Kiro account.
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	token, err := p.tokenMgr.GetToken()
	if err != nil {
		return nil, fmt.Errorf("token error: %w", err)
	}
	return p.client.ListModels(ctx, p.profileArn, token)
}

// ListModelDetails fetches full model metadata available to this Kiro account.
func (p *Provider) ListModelDetails(ctx context.Context) ([]ModelInfo, error) {
	token, err := p.tokenMgr.GetToken()
	if err != nil {
		return nil, fmt.Errorf("token error: %w", err)
	}
	return p.client.ListModelDetails(ctx, p.profileArn, token)
}

func (c *CWClient) GetUsageLimits(ctx context.Context, account, profileArn string, token *TokenInfo) (*UsageLimits, error) {
	reqBody := []byte("{}")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.restEndpoint()+"?origin=KIRO_CLI&resourceType=AGENTIC_REQUEST&isEmailRequired=true", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create usage request: %w", err)
	}
	setKiroRestHeaders(req, token, kiroRestTarget)
	debugKiroHTTPRequest(c.logger, "kiro usage request", req, reqBody)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usage request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return nil, fmt.Errorf("read usage response: %w", err)
	}
	debugKiroHTTPResponse(c.logger, "kiro usage response", resp, body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage API returned %d: %s", resp.StatusCode, truncateForError(string(body), 300))
	}

	var result usageLimitsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}

	limits := &UsageLimits{
		Account:        account,
		ProfileArn:     profileArn,
		DaysUntilReset: result.DaysUntilReset,
		FetchedAt:      time.Now(),
	}
	if result.NextDateReset != nil {
		nextDateReset := int64(*result.NextDateReset)
		limits.NextDateReset = &nextDateReset
	}
	if result.UserInfo != nil {
		limits.Email = result.UserInfo.Email
	}
	if result.SubscriptionInfo != nil {
		limits.RawSubscription = result.SubscriptionInfo.Type
		limits.SubscriptionName = result.SubscriptionInfo.SubscriptionName
		limits.SubscriptionTitle = result.SubscriptionInfo.SubscriptionTitle
		limits.SubscriptionType = result.SubscriptionInfo.SubscriptionType
		limits.SubscriptionState = result.SubscriptionInfo.Status
		limits.Tier = firstNonEmpty(result.SubscriptionInfo.SubscriptionTitle, result.SubscriptionInfo.SubscriptionName, result.SubscriptionInfo.SubscriptionType, result.SubscriptionInfo.Type)
	}
	if len(result.UsageBreakdownList) > 0 {
		breakdown := result.UsageBreakdownList[0]
		for _, b := range result.UsageBreakdownList {
			if b.ResourceType == "CREDIT" {
				breakdown = b
				break
			}
		}
		usedPrecise := firstPositive(breakdown.CurrentUsageWithPrecision, breakdown.CurrentUsage)
		limitPrecise := firstPositive(breakdown.UsageLimitWithPrecision, breakdown.UsageLimit)
		limits.Usage = UsageQuota{
			ResourceType:     breakdown.ResourceType,
			DisplayName:      firstNonEmpty(breakdown.DisplayName, breakdown.DisplayNamePlural),
			Used:             breakdown.CurrentUsage,
			Limit:            breakdown.UsageLimit,
			UsedPrecise:      usedPrecise,
			LimitPrecise:     limitPrecise,
			Remaining:        maxFloat(0, breakdown.UsageLimit-breakdown.CurrentUsage),
			RemainingPrecise: maxFloat(0, limitPrecise-usedPrecise),
			OverageRate:      breakdown.OverageRate,
			OverageCap:       breakdown.OverageCap,
			Overages:         breakdown.CurrentOverages,
			Currency:         breakdown.Currency,
		}
		if limitPrecise > 0 {
			limits.Usage.PercentUsed = usedPrecise / limitPrecise * 100
		}
	}

	return limits, nil
}

func (c *CWClient) ListModels(ctx context.Context, profileArn string, token *TokenInfo) ([]string, error) {
	details, err := c.ListModelDetails(ctx, profileArn, token)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(details))
	for _, model := range details {
		if model.ModelID != "" {
			models = append(models, model.ModelID)
		}
	}
	return models, nil
}

func (c *CWClient) ListModelDetails(ctx context.Context, profileArn string, token *TokenInfo) ([]ModelInfo, error) {
	req, body, err := c.newListModelsRequest(ctx, profileArn)
	if err != nil {
		return nil, err
	}
	setKiroRestHeaders(req, token, kiroModelsTarget)
	debugKiroHTTPRequest(c.logger, "kiro models request", req, body)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}
	debugKiroHTTPResponse(c.logger, "kiro models response", resp, respBody)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models API returned %d: %s", resp.StatusCode, truncateForError(string(respBody), 300))
	}

	var result listModelsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse models response: %w", err)
	}
	defaultID := strings.TrimSpace(result.DefaultModel.ModelID)
	if defaultID == "" {
		defaultID = strings.TrimSpace(result.DefaultModel.ModelName)
	}
	models := make([]ModelInfo, 0, len(result.Models))
	seen := make(map[string]bool, len(result.Models))
	for _, model := range result.Models {
		id := strings.TrimSpace(model.ModelID)
		if id == "" {
			id = strings.TrimSpace(model.ModelName)
		}
		if id != "" && !seen[id] {
			models = append(models, model.toModelInfo(defaultID))
			seen[id] = true
		}
	}
	return models, nil
}

type listModelsResponse struct {
	DefaultModel rawModelInfo   `json:"defaultModel"`
	Models       []rawModelInfo `json:"models"`
}

type rawModelInfo struct {
	ModelID             string   `json:"modelId"`
	ModelName           string   `json:"modelName"`
	Description         string   `json:"description"`
	RateMultiplier      float64  `json:"rateMultiplier"`
	RateUnit            string   `json:"rateUnit"`
	SupportedInputTypes []string `json:"supportedInputTypes"`
	TokenLimits         *struct {
		MaxInputTokens  int `json:"maxInputTokens"`
		MaxOutputTokens int `json:"maxOutputTokens"`
	} `json:"tokenLimits"`
	PromptCaching *struct {
		SupportsPromptCaching bool `json:"supportsPromptCaching"`
	} `json:"promptCaching"`
}

func (m rawModelInfo) toModelInfo(defaultID string) ModelInfo {
	id := strings.TrimSpace(m.ModelID)
	if id == "" {
		id = strings.TrimSpace(m.ModelName)
	}
	info := ModelInfo{
		ModelID:             id,
		ModelName:           strings.TrimSpace(m.ModelName),
		Description:         strings.TrimSpace(m.Description),
		RateMultiplier:      m.RateMultiplier,
		RateUnit:            m.RateUnit,
		SupportedInputTypes: m.SupportedInputTypes,
		IsDefault:           id != "" && id == defaultID,
	}
	if info.ModelName == "" {
		info.ModelName = id
	}
	if m.TokenLimits != nil {
		info.TokenLimits = &TokenLimits{
			MaxInputTokens:  m.TokenLimits.MaxInputTokens,
			MaxOutputTokens: m.TokenLimits.MaxOutputTokens,
		}
	}
	if m.PromptCaching != nil {
		info.PromptCaching = &PromptCache{SupportsPromptCaching: m.PromptCaching.SupportsPromptCaching}
	}
	return info
}

func (c *CWClient) newListModelsRequest(ctx context.Context, profileArn string) (*http.Request, []byte, error) {
	payload := map[string]any{
		"origin": "KIRO_CLI",
	}
	query := url.Values{}
	query.Set("origin", "KIRO_CLI")
	if strings.TrimSpace(profileArn) != "" {
		payload["profileArn"] = profileArn
		query.Set("profileArn", profileArn)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal models request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.restEndpoint()+"?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("create models request: %w", err)
	}
	return req, body, nil
}

func (c *CWClient) restEndpoint() string {
	return fmt.Sprintf("https://q.%s.amazonaws.com/", normalizeRegion(c.apiRegion))
}

func setKiroRestHeaders(req *http.Request, token *TokenInfo, target string) {
	apiName := "codewhispererruntime"
	ua := fmt.Sprintf("aws-sdk-rust/1.3.14 ua/2.1 api/%s/0.1.14474 os/%s lang/rust/1.92.0 md/appVersion-1.28.1 app/AmazonQ-For-CLI", apiName, runtime.GOOS)
	xAmzUA := fmt.Sprintf("aws-sdk-rust/1.3.14 ua/2.1 api/%s/0.1.14474 os/%s lang/rust/1.92.0 m/F,C app/AmazonQ-For-CLI", apiName, runtime.GOOS)

	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("X-Amz-User-Agent", xAmzUA)
	req.Header.Set("X-Amzn-Codewhisperer-Optout", "false")
	req.Header.Set("Amz-Sdk-Invocation-Id", uuid.New().String())
	req.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
	req.Header.Set("Accept", "*/*")
	if token.IsExternalIdP {
		req.Header.Set("TokenType", "EXTERNAL_IDP")
	}
}

func truncateForError(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
