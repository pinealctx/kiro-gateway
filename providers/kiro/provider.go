package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pinealctx/kiro-gateway/core/converter"
	"github.com/pinealctx/kiro-gateway/core/providers"
	"github.com/pinealctx/kiro-gateway/core/sanitizer"
	"github.com/pinealctx/kiro-gateway/core/thinking"
	"github.com/pinealctx/kiro-gateway/models"
	"go.uber.org/zap"
)

// KVStore is a minimal interface for key-value persistence (implemented by tenant.Store).
type KVStore interface {
	GetKV(key string) (string, bool)
	SetKV(key, value string) error
}

// Provider implements the AIProvider interface for Kiro/CodeWhisperer.
type Provider struct {
	name       string
	tokenMgr   *TokenManager
	client     *CWClient
	authMgr    *KiroAuthManager
	logger     *zap.Logger
	profileArn string
	region     string
	kvStore    KVStore
	stopCh     chan struct{}
}

// NewProvider creates a Kiro provider using the built-in PKCE login flow.
func NewProvider(name string, logger *zap.Logger, region ...string) *Provider {
	tm := NewTokenManager(logger)
	r := "us-east-1"
	if len(region) > 0 {
		r = normalizeRegion(region[0])
	}

	p := &Provider{
		name:     name,
		tokenMgr: tm,
		client:   NewCWClient(logger, r),
		authMgr:  NewKiroAuthManager(logger),
		logger:   logger,
		region:   r,
		stopCh:   make(chan struct{}),
	}

	// Re-persist token after each background refresh so the KV store always
	// holds the latest RefreshToken (important when the IdP rotates tokens).
	tm.SetOnRefresh(func(lt *LoginToken) {
		p.persistToken(lt)
	})

	tm.StartBackgroundRefreshWithStop(2*time.Minute, p.stopCh)

	return p
}

// kvKeyToken returns the provider-specific key for token persistence.
func (p *Provider) kvKeyToken() string {
	return "kiro:" + p.name + ":token"
}

// AuthMgr returns the Kiro auth manager for PKCE login management.
func (p *Provider) AuthMgr() *KiroAuthManager {
	return p.authMgr
}

// SetStore injects a KV store for token persistence.
func (p *Provider) SetStore(store KVStore) {
	p.kvStore = store
}

// SetLoginToken injects a token from the built-in PKCE login.
func (p *Provider) SetLoginToken(lt *LoginToken) {
	p.tokenMgr.SetLoginToken(lt)
	p.profileArn = lt.ProfileArn
	p.persistToken(lt)
}

// persistedToken is the JSON-serializable form stored in the DB.
type persistedToken struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	ClientID      string    `json:"client_id"`
	ClientSecret  string    `json:"client_secret,omitempty"` // non-empty for AWS OIDC (Builder ID)
	TokenEndpoint string    `json:"token_endpoint"`
	ExpiresAt     time.Time `json:"expires_at"`
	IsExternalIdP bool      `json:"is_external_idp"`
	RefreshScope  string    `json:"refresh_scope,omitempty"`
	ProfileArn    string    `json:"profile_arn,omitempty"`
}

func (p *Provider) persistToken(lt *LoginToken) {
	if p.kvStore == nil {
		return
	}
	pt := persistedToken{
		AccessToken:   lt.AccessToken,
		RefreshToken:  lt.RefreshToken,
		ClientID:      lt.ClientID,
		ClientSecret:  lt.ClientSecret,
		TokenEndpoint: lt.TokenEndpoint,
		ExpiresAt:     lt.ExpiresAt,
		IsExternalIdP: lt.IsExternalIdP,
		RefreshScope:  lt.RefreshScope,
		ProfileArn:    lt.ProfileArn,
	}
	data, err := json.Marshal(pt)
	if err != nil {
		p.logger.Error("failed to marshal token for persistence", zap.Error(err))
		return
	}
	if err := p.kvStore.SetKV(p.kvKeyToken(), string(data)); err != nil {
		p.logger.Error("failed to persist kiro token", zap.Error(err))
	}
}

// RestoreToken loads a previously persisted token from the KV store.
// Returns true if a token was successfully restored.
func (p *Provider) RestoreToken() bool {
	if p.kvStore == nil {
		return false
	}
	data, ok := p.kvStore.GetKV(p.kvKeyToken())
	if !ok || data == "" {
		return false
	}
	var pt persistedToken
	if err := json.Unmarshal([]byte(data), &pt); err != nil {
		p.logger.Error("failed to unmarshal persisted token", zap.Error(err))
		return false
	}

	lt := &LoginToken{
		AccessToken:   pt.AccessToken,
		RefreshToken:  pt.RefreshToken,
		ClientID:      pt.ClientID,
		ClientSecret:  pt.ClientSecret,
		TokenEndpoint: pt.TokenEndpoint,
		ExpiresAt:     pt.ExpiresAt,
		IsExternalIdP: pt.IsExternalIdP,
		RefreshScope:  pt.RefreshScope,
		ProfileArn:    pt.ProfileArn,
	}
	p.tokenMgr.SetLoginToken(lt)
	p.profileArn = pt.ProfileArn
	p.logger.Info("kiro token restored from DB",
		zap.Bool("has_refresh", pt.RefreshToken != ""),
		zap.String("profile_arn", pt.ProfileArn),
		zap.Time("expires_at", pt.ExpiresAt))
	return true
}

// ForceRefresh forces a token refresh and re-persists the updated token.
func (p *Provider) ForceRefresh() error {
	_, err := p.tokenMgr.ForceRefresh()
	if err != nil {
		return err
	}
	// Re-persist the updated login token
	p.tokenMgr.mu.RLock()
	lt := p.tokenMgr.loginToken
	p.tokenMgr.mu.RUnlock()
	if lt != nil {
		p.persistToken(lt)
	}
	return nil
}

// TokenStatus returns information about the current token state.
func (p *Provider) TokenStatus() map[string]any {
	p.tokenMgr.mu.RLock()
	defer p.tokenMgr.mu.RUnlock()

	status := map[string]any{
		"has_login":   p.tokenMgr.loginToken != nil,
		"has_current": p.tokenMgr.current != nil,
		"profile_arn": p.profileArn,
		"region":      p.region,
	}
	if p.tokenMgr.current != nil {
		status["expires_at"] = p.tokenMgr.current.ExpiresAt
		status["is_external_idp"] = p.tokenMgr.current.IsExternalIdP
	}
	return status
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) ChatCompletion(ctx context.Context, req *models.ChatCompletionRequest) (*models.ChatCompletionResponse, error) {
	// Use streaming internally and collect the full response
	stream := make(chan providers.StreamChunk, 64)
	go func() {
		_ = p.StreamCompletion(ctx, req, stream)
	}()

	var fullContent string
	var reasoningContent string
	var toolCalls []models.ToolCall
	var finishReason string

	for chunk := range stream {
		if chunk.Error != nil {
			return nil, chunk.Error
		}
		fullContent += chunk.Content
		reasoningContent += chunk.ReasoningContent
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}
		if chunk.FinishReason != "" {
			finishReason = chunk.FinishReason
		}
	}

	if finishReason == "" {
		finishReason = "stop"
	}

	return &models.ChatCompletionResponse{
		ID:      "chatcmpl-" + uuid.New().String()[:8],
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []models.ChatCompletionChoice{
			{
				Index: 0,
				Message: models.ChatMessage{
					Role:             "assistant",
					Content:          models.RawString(fullContent),
					ReasoningContent: reasoningContent,
					ToolCalls:        toolCalls,
				},
				FinishReason: finishReason,
			},
		},
	}, nil
}

func (p *Provider) StreamCompletion(ctx context.Context, req *models.ChatCompletionRequest, stream chan<- providers.StreamChunk) error {
	defer close(stream)

	token, err := p.tokenMgr.GetToken()
	if err != nil {
		stream <- providers.StreamChunk{Error: fmt.Errorf("token error: %w", err)}
		return err
	}

	cwReq, err := converter.OpenAIToCW(req, p.profileArn)
	if err != nil {
		stream <- providers.StreamChunk{Error: fmt.Errorf("conversion error: %w", err)}
		return err
	}

	events, err := p.client.GenerateStream(ctx, cwReq, token)
	if err != nil {
		stream <- providers.StreamChunk{Error: fmt.Errorf("cw stream error: %w", err)}
		return err
	}

	thinkCfg := thinking.ParseConfig(req.Extras)
	var parser *thinking.Parser
	if thinkCfg.Enabled {
		parser = thinking.NewParser()
	}
	clientToolNames := sanitizer.ClientToolNameSet(openAIToolsAsMaps(req.Tools))

	for evt := range events {
		switch evt.Type {
		case "text":
			sanitized := sanitizer.SanitizeText(evt.Content, true)
			if sanitized != "" {
				if parser != nil {
					for _, seg := range parser.Push(sanitized) {
						if seg.Type == thinking.SegmentThinking {
							stream <- providers.StreamChunk{ReasoningContent: seg.Text}
						} else {
							stream <- providers.StreamChunk{Content: seg.Text}
						}
					}
				} else {
					stream <- providers.StreamChunk{Content: sanitized}
				}
			}

		case "reasoning":
			if evt.Reasoning != "" {
				stream <- providers.StreamChunk{ReasoningContent: evt.Reasoning}
			}

		case "tool_use":
			if evt.ToolUse == nil {
				continue
			}

			toolName := evt.ToolUse.Name
			toolInput := evt.ToolUse.Input
			if sanitizer.IsBuiltinTool(toolName) {
				remappedName, remappedInput, ok := sanitizer.RemapBuiltinTool(toolName, toolInput, clientToolNames)
				if !ok {
					p.logger.Debug("filtered kiro builtin tool call", zap.String("tool", toolName))
					continue
				}
				p.logger.Debug("remapped kiro builtin tool call",
					zap.String("from", toolName),
					zap.String("to", remappedName))
				toolName = remappedName
				toolInput = remappedInput
			}

			if toolName != "" {
				input := toolInput
				if input == nil {
					input = map[string]any{}
				}
				inputJSON, err := json.Marshal(input)
				if err != nil || string(inputJSON) == "null" {
					inputJSON = []byte("{}")
				}
				stream <- providers.StreamChunk{
					ToolCalls: []models.ToolCall{
						{
							ID:   evt.ToolUse.ToolUseID,
							Type: "function",
							Function: models.ToolCallFunction{
								Name:      toolName,
								Arguments: string(inputJSON),
							},
						},
					},
				}
			}

		case "context_usage":
			if evt.ContextUsage > 0.95 {
				stream <- providers.StreamChunk{FinishReason: "length"}
			}

		case "exception":
			if evt.Error != nil {
				stream <- providers.StreamChunk{Error: evt.Error}
			}

		case "end":
			// Stream complete
		}
	}

	if parser != nil {
		for _, seg := range parser.Flush() {
			if seg.Type == thinking.SegmentThinking {
				stream <- providers.StreamChunk{ReasoningContent: seg.Text}
			} else {
				stream <- providers.StreamChunk{Content: seg.Text}
			}
		}
	}

	return nil
}

func alignHistoryStart(history *[]models.CWHistoryEntry) {
	for len(*history) > 0 && (*history)[0].UserInputMessage == nil {
		*history = (*history)[1:]
	}
}

func repairOrphanedToolResults(history *[]models.CWHistoryEntry) {
	for i := range *history {
		user := (*history)[i].UserInputMessage
		if user == nil || user.UserInputMessageContext == nil || len(user.UserInputMessageContext.ToolResults) == 0 {
			continue
		}
		validIDs := map[string]bool{}
		if i > 0 {
			if prev := (*history)[i-1].AssistantResponseMessage; prev != nil {
				for _, tu := range prev.ToolUses {
					if tu.ToolUseID != "" {
						validIDs[tu.ToolUseID] = true
					}
				}
			}
		}
		kept := user.UserInputMessageContext.ToolResults[:0]
		var orphaned []string
		for _, tr := range user.UserInputMessageContext.ToolResults {
			if validIDs[tr.ToolUseID] {
				kept = append(kept, tr)
				continue
			}
			for _, part := range tr.Content {
				if part.Text != "" {
					orphaned = append(orphaned, part.Text)
				}
			}
		}
		user.UserInputMessageContext.ToolResults = kept
		if len(kept) == 0 {
			user.UserInputMessageContext.ToolResults = nil
			if len(user.UserInputMessageContext.Tools) == 0 {
				user.UserInputMessageContext = nil
			}
		}
		if len(orphaned) > 0 {
			user.Content += "\n[trimmed tool result] " + strings.Join(orphaned, "; ")
		}
	}
}

func (p *Provider) RefreshToken(ctx context.Context) error {
	_, err := p.tokenMgr.refresh()
	return err
}

func (p *Provider) IsHealthy(ctx context.Context) bool {
	_, err := p.tokenMgr.GetToken()
	return err == nil
}

// GetTokenInfo returns current token status for admin display.
func (p *Provider) GetTokenInfo() map[string]any {
	hasToken := p.tokenMgr.HasToken()
	info := map[string]any{
		"healthy":   hasToken,
		"has_token": hasToken,
		"region":    p.region,
	}
	if p.profileArn != "" {
		info["profile_arn"] = p.profileArn
	}
	return info
}

func normalizeRegion(region string) string {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return "us-east-1"
	}
	return region
}

// Stop shuts down the provider's background goroutines.
func (p *Provider) Stop() {
	close(p.stopCh)
}

func openAIToolsAsMaps(tools []models.Tool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		fn := map[string]any{
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
		}
		if len(tool.Function.Parameters) > 0 {
			var params any
			if err := json.Unmarshal(tool.Function.Parameters, &params); err == nil {
				fn["parameters"] = params
			}
		}
		result = append(result, map[string]any{
			"type":     tool.Type,
			"function": fn,
		})
	}
	return result
}
