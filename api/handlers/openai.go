package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pinealctx/kiro-gateway/core/continuation"
	"github.com/pinealctx/kiro-gateway/core/providers"
	"github.com/pinealctx/kiro-gateway/core/streaming"
	"github.com/pinealctx/kiro-gateway/middleware"
	"github.com/pinealctx/kiro-gateway/models"
	"github.com/pinealctx/kiro-gateway/tenant"
	"go.uber.org/zap"
)

// OpenAIHandler handles /v1/chat/completions requests.
type OpenAIHandler struct {
	registry *providers.Registry
	store    *tenant.Store
	logger   *zap.Logger
}

func NewOpenAIHandler(registry *providers.Registry, store *tenant.Store, logger *zap.Logger) *OpenAIHandler {
	return &OpenAIHandler{registry: registry, store: store, logger: logger}
}

func (h *OpenAIHandler) ChatCompletions(c *gin.Context) {
	var req models.ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": "Invalid request body: " + err.Error(), "type": "invalid_request_error"},
		})
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": "model is required", "type": "invalid_request_error"},
		})
		return
	}

	account, allowed := middleware.ResolveKiroAccount(c, c.Param("kiro_account"))
	if !allowed {
		return
	}
	provider, cleanModel, ok := resolveKiroAccount(h.registry, req.Model, account)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"message": "No available account for API key/model: " + req.Model, "type": "server_error"},
		})
		return
	}
	req.Model = cleanModel
	if !middleware.CheckModelPermission(c, cleanModel) {
		return
	}

	reqID := uuid.New().String()[:8]

	suppressReasoning := middleware.SuppressReasoning(c)

	if req.Stream {
		h.handleStream(c, provider, &req, reqID, suppressReasoning)
	} else {
		h.handleNonStream(c, provider, &req, reqID, suppressReasoning)
	}
}

func (h *OpenAIHandler) handleNonStream(c *gin.Context, provider providers.AIProvider, req *models.ChatCompletionRequest, _ string, suppressReasoning bool) {
	start := time.Now()
	resp, err := provider.ChatCompletion(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"message": err.Error(), "type": "upstream_error"},
		})
		return
	}
	for i := range resp.Choices {
		if suppressReasoning {
			resp.Choices[i].Message.ReasoningContent = ""
		}
		for j := range resp.Choices[i].Message.ToolCalls {
			resp.Choices[i].Message.ToolCalls[j].ID = ensureOpenAIToolCallID(resp.Choices[i].Message.ToolCalls[j].ID)
		}
	}
	inputTokens, outputTokens := estimateOpenAITokens(req.Messages, resp)
	recordUsage(h.store, c, req.Model, provider.Name(), inputTokens, outputTokens, start)
	c.JSON(http.StatusOK, resp)
}

func (h *OpenAIHandler) handleStream(c *gin.Context, provider providers.AIProvider, req *models.ChatCompletionRequest, reqID string, suppressReasoning bool) {
	start := time.Now()
	completionID := "chatcmpl-" + reqID
	writer := streaming.NewOpenAISSEWriter(c, req.Model, completionID)

	if err := writer.WriteRoleDelta(); err != nil {
		return
	}

	var fullOutput string
	var hasToolCalls bool
	continueCount := 0

	for continueCount <= continuation.MaxContinuations {
		stream := make(chan providers.StreamChunk, 64)
		go func() {
			if err := provider.StreamCompletion(c.Request.Context(), req, stream); err != nil {
				h.logger.Error("stream completion error", zap.Error(err))
			}
		}()

		truncated := false
		for chunk := range stream {
			if chunk.Error != nil {
				h.logger.Error("stream error", zap.Error(chunk.Error))
				_ = writer.WriteContentDelta("\n\n[Error: " + chunk.Error.Error() + "]")
				_ = writer.WriteFinish("stop")
				return
			}

			if chunk.Content != "" {
				fullOutput += chunk.Content
				if err := writer.WriteContentDelta(chunk.Content); err != nil {
					return
				}
			}

			if chunk.ReasoningContent != "" && !suppressReasoning {
				if err := writer.WriteReasoningDelta(chunk.ReasoningContent); err != nil {
					return
				}
			}

			if len(chunk.ToolCalls) > 0 {
				hasToolCalls = true
				for i := range chunk.ToolCalls {
					chunk.ToolCalls[i].ID = ensureOpenAIToolCallID(chunk.ToolCalls[i].ID)
				}
				if err := writer.WriteToolCallDelta(chunk.ToolCalls); err != nil {
					return
				}
			}

			if chunk.FinishReason == "length" {
				truncated = true
			}
		}

		// Check if we should auto-continue
		if truncated && continueCount < continuation.MaxContinuations && continuation.ShouldAutoContinue(fullOutput, req.Messages) {
			continueCount++
			h.logger.Info("auto-continuing", zap.Int("round", continueCount))
			req.Messages = continuation.BuildContinuationMessages(req.Messages, fullOutput)
			continue
		}

		break
	}

	finishReason := "stop"
	if hasToolCalls {
		finishReason = "tool_calls"
	}
	recordUsage(h.store, c, req.Model, provider.Name(), estimateInputTokens(req.Messages), len(fullOutput)/4, start)
	_ = writer.WriteFinish(finishReason)
}

// Models handles /v1/models — returns available models.
func (h *OpenAIHandler) Models(c *gin.Context) {
	account, ok := middleware.ResolveKiroAccount(c, c.Param("kiro_account"))
	if !ok {
		return
	}
	provider, _, ok := resolveKiroAccount(h.registry, "", account)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"message": "No available Kiro account for models", "type": "server_error"},
		})
		return
	}
	lister, ok := provider.(providers.ModelLister)
	if !ok {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"message": "Resolved account does not support model listing", "type": "upstream_error"},
		})
		return
	}
	modelIDs, err := lister.ListModels(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"message": err.Error(), "type": "upstream_error"},
		})
		return
	}

	now := time.Now().Unix()
	createdAt := time.Now().UTC().Format(time.RFC3339)

	modelList := make([]gin.H, 0, len(modelIDs))
	for _, id := range modelIDs {
		apiID := claudeCodeModelID(id)
		modelList = append(modelList, gin.H{
			"id":            apiID,
			"display_name":  id,
			"kiro_model_id": id,
			"type":          "model",
			"object":        "model",
			"created":       now,
			"created_at":    createdAt,
			"owned_by":      provider.Name(),
		})
	}

	resp := gin.H{
		"object": "list",
		"data":   modelList,
	}
	if len(modelList) > 0 {
		resp["first_id"] = modelList[0]["id"]
		resp["last_id"] = modelList[len(modelList)-1]["id"]
		resp["has_more"] = false
	}
	c.JSON(http.StatusOK, resp)
}

func estimateOpenAITokens(reqMessages []models.ChatMessage, resp *models.ChatCompletionResponse) (int, int) {
	if resp != nil && resp.Usage != nil {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	input := estimateInputTokens(reqMessages)
	outputChars := 0
	if resp != nil && len(resp.Choices) > 0 {
		outputChars += len(models.ContentText(resp.Choices[0].Message.Content))
		outputChars += len(resp.Choices[0].Message.ReasoningContent)
		for _, tc := range resp.Choices[0].Message.ToolCalls {
			outputChars += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return input, outputChars / 4
}

func ensureOpenAIToolCallID(id string) string {
	if id != "" {
		return id
	}
	return "call_" + uuid.New().String()[:24]
}
