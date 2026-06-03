package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pinealctx/kiro-gateway/core/continuation"
	"github.com/pinealctx/kiro-gateway/core/converter"
	"github.com/pinealctx/kiro-gateway/core/providers"
	"github.com/pinealctx/kiro-gateway/core/streaming"
	"github.com/pinealctx/kiro-gateway/middleware"
	"github.com/pinealctx/kiro-gateway/models"
	"github.com/pinealctx/kiro-gateway/tenant"
	"go.uber.org/zap"
)

// AnthropicHandler handles /v1/messages requests.
type AnthropicHandler struct {
	registry *providers.Registry
	store    *tenant.Store
	logger   *zap.Logger
}

func NewAnthropicHandler(registry *providers.Registry, store *tenant.Store, logger *zap.Logger) *AnthropicHandler {
	return &AnthropicHandler{registry: registry, store: store, logger: logger}
}

func (h *AnthropicHandler) Messages(c *gin.Context) {
	var req models.AnthropicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":  "error",
			"error": gin.H{"type": "invalid_request_error", "message": "Invalid request body: " + err.Error()},
		})
		return
	}
	req.Model = normalizeGatewayModelID(req.Model)
	if strings.TrimSpace(req.Model) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":  "error",
			"error": gin.H{"type": "invalid_request_error", "message": "model is required"},
		})
		return
	}

	// Convert Anthropic → OpenAI format
	openaiReq, err := converter.AnthropicToOpenAI(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":  "error",
			"error": gin.H{"type": "invalid_request_error", "message": err.Error()},
		})
		return
	}

	account, allowed := middleware.ResolveKiroAccount(c, c.Param("kiro_account"))
	if !allowed {
		return
	}
	provider, cleanModel, ok := resolveKiroAccount(h.registry, openaiReq.Model, account)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":  "error",
			"error": gin.H{"type": "api_error", "message": "No available account for API key/model: " + req.Model},
		})
		return
	}
	openaiReq.Model = cleanModel
	if !middleware.CheckModelPermission(c, cleanModel) {
		return
	}

	reqID := "msg_" + uuid.New().String()[:8]

	suppressReasoning := middleware.SuppressReasoning(c)

	if req.Stream {
		h.handleStream(c, provider, openaiReq, req.Model, reqID, suppressReasoning)
	} else {
		h.handleNonStream(c, provider, openaiReq, req.Model, reqID, suppressReasoning)
	}
}

// setAnthropicHeaders sets standard Anthropic response headers expected by Claude Code SDK.
func setAnthropicHeaders(c *gin.Context, reqID string) {
	c.Header("anthropic-version", "2023-06-01")
	c.Header("request-id", reqID)
	c.Header("x-request-id", reqID)
}

// estimateInputTokens provides a rough token estimate for message_start usage.
func estimateInputTokens(messages []models.ChatMessage) int {
	totalChars := 0
	for _, msg := range messages {
		totalChars += len(models.ContentText(msg.Content))
	}
	// ~4 chars per token + per-message overhead
	return totalChars/4 + len(messages)*4
}

func (h *AnthropicHandler) handleNonStream(c *gin.Context, provider providers.AIProvider, req *models.ChatCompletionRequest, model, reqID string, suppressReasoning bool) {
	setAnthropicHeaders(c, reqID)
	start := time.Now()

	resp, err := provider.ChatCompletion(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"type":  "error",
			"error": gin.H{"type": "api_error", "message": err.Error()},
		})
		return
	}

	// Convert OpenAI response → Anthropic format
	anthropicResp := models.AnthropicResponse{
		ID:    reqID,
		Type:  "message",
		Role:  "assistant",
		Model: model,
		Usage: models.AnthropicUsage{},
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		if !suppressReasoning && choice.Message.ReasoningContent != "" {
			anthropicResp.Content = append(anthropicResp.Content, models.AnthropicContentBlock{
				Type:     "thinking",
				Thinking: choice.Message.ReasoningContent,
			})
		}
		if content := models.ContentText(choice.Message.Content); content != "" {
			anthropicResp.Content = append(anthropicResp.Content, models.AnthropicContentBlock{
				Type: "text",
				Text: content,
			})
		}
		// Convert tool_calls to Anthropic tool_use content blocks
		for _, tc := range choice.Message.ToolCalls {
			inputRaw := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(inputRaw) || len(inputRaw) == 0 {
				inputRaw = json.RawMessage(`{}`)
			}
			anthropicResp.Content = append(anthropicResp.Content, models.AnthropicContentBlock{
				Type:  "tool_use",
				ID:    ensureAnthropicToolUseID(tc.ID),
				Name:  tc.Function.Name,
				Input: inputRaw,
			})
		}
		switch choice.FinishReason {
		case "stop":
			anthropicResp.StopReason = "end_turn"
		case "tool_calls":
			anthropicResp.StopReason = "tool_use"
		case "length":
			anthropicResp.StopReason = "max_tokens"
		default:
			anthropicResp.StopReason = "end_turn"
		}
	}
	inputTokens, outputTokens := estimateOpenAITokens(req.Messages, resp)
	anthropicResp.Usage.InputTokens = inputTokens
	anthropicResp.Usage.OutputTokens = outputTokens
	recordUsage(h.store, c, req.Model, provider.Name(), inputTokens, outputTokens, start)

	c.JSON(http.StatusOK, anthropicResp)
}

func (h *AnthropicHandler) handleStream(c *gin.Context, provider providers.AIProvider, req *models.ChatCompletionRequest, model, reqID string, suppressReasoning bool) {
	setAnthropicHeaders(c, reqID)
	start := time.Now()

	inputTokens := estimateInputTokens(req.Messages)
	writer := streaming.NewAnthropicSSEWriter(c, model, reqID)

	if err := writer.WriteMessageStart(inputTokens); err != nil {
		return
	}

	var fullOutput string
	var thinkingOutput string
	textStarted := false
	textClosed := false
	thinkingStarted := false
	thinkingClosed := false
	hasToolUse := false
	emittedMeaningfulText := false
	outputTruncated := false
	streamFailed := false
	continueCount := 0
	toolBlockOpen := false // track whether a tool_use content block is open

	for continueCount <= continuation.MaxContinuations {
		stream := make(chan providers.StreamChunk, 64)
		go func() {
			if err := provider.StreamCompletion(c.Request.Context(), req, stream); err != nil {
				if c.Request.Context().Err() != nil {
					h.logger.Debug("stream completion canceled (client disconnected)",
						zap.Error(err),
						zap.String("request_id", reqID),
						zap.String("requested_model", model),
						zap.String("upstream_model", req.Model),
						zap.String("provider", provider.Name()),
					)
				} else {
					h.logger.Error("stream completion error",
						zap.Error(err),
						zap.String("request_id", reqID),
						zap.String("requested_model", model),
						zap.String("upstream_model", req.Model),
						zap.String("provider", provider.Name()),
					)
				}
			}
		}()

		truncated := false
		for chunk := range stream {
			if chunk.Error != nil {
				if c.Request.Context().Err() != nil {
					h.logger.Debug("stream chunk error (client disconnected)",
						zap.Error(chunk.Error),
						zap.String("request_id", reqID),
						zap.String("requested_model", model),
						zap.String("upstream_model", req.Model),
						zap.String("provider", provider.Name()),
					)
					break
				}
				h.logger.Error("stream error",
					zap.Error(chunk.Error),
					zap.String("request_id", reqID),
					zap.String("requested_model", model),
					zap.String("upstream_model", req.Model),
					zap.String("provider", provider.Name()),
				)
				if err := writer.WriteError("api_error", chunk.Error.Error()); err != nil {
					return
				}
				streamFailed = true
				break
			}

			if chunk.Content != "" {
				// Close tool block if open before starting text
				if toolBlockOpen {
					_ = writer.WriteContentBlockStop()
					toolBlockOpen = false
				}
				if thinkingStarted && !thinkingClosed {
					if err := writer.WriteContentBlockStop(); err != nil {
						return
					}
					thinkingStarted = false
					thinkingClosed = false
				}
				if !textStarted {
					if err := writer.WriteContentBlockStart(); err != nil {
						return
					}
					textStarted = true
				}
				fullOutput += chunk.Content
				if strings.TrimSpace(chunk.Content) != "" {
					emittedMeaningfulText = true
				}
				if err := writer.WriteContentDelta(chunk.Content); err != nil {
					return
				}
			}

			if chunk.ReasoningContent != "" && !suppressReasoning {
				if toolBlockOpen {
					_ = writer.WriteContentBlockStop()
					toolBlockOpen = false
				}
				if textStarted && !textClosed {
					if err := writer.WriteContentBlockStop(); err != nil {
						return
					}
					textStarted = false
					textClosed = false
				}
				if !thinkingStarted {
					if err := writer.WriteThinkingBlockStart(); err != nil {
						return
					}
					thinkingStarted = true
				}
				fullOutput += chunk.ReasoningContent
				thinkingOutput += chunk.ReasoningContent
				if err := writer.WriteThinkingDelta(chunk.ReasoningContent); err != nil {
					return
				}
			}

			if len(chunk.ToolCalls) > 0 {
				hasToolUse = true

				// Close text block if open
				if textStarted && !textClosed {
					if err := writer.WriteContentBlockStop(); err != nil {
						return
					}
					textStarted = false
					textClosed = false
				}
				if thinkingStarted && !thinkingClosed {
					if err := writer.WriteContentBlockStop(); err != nil {
						return
					}
					thinkingStarted = false
					thinkingClosed = false
				}

				for _, tc := range chunk.ToolCalls {
					// tc.ID != "" means a new tool_call is starting
					// (subsequent deltas for the same call have empty ID)
					if tc.ID != "" || tc.Function.Name != "" {
						// Close previous tool block if open
						if toolBlockOpen {
							if err := writer.WriteContentBlockStop(); err != nil {
								return
							}
						}
						toolID := ensureAnthropicToolUseID(tc.ID)
						if err := writer.WriteToolUseBlockStart(toolID, tc.Function.Name); err != nil {
							return
						}
						toolBlockOpen = true
					}

					// Append arguments delta to current block
					if tc.Function.Arguments != "" {
						if err := writer.WriteToolUseInputDelta(tc.Function.Arguments); err != nil {
							return
						}
					}
				}
			}

			if chunk.FinishReason != "" {
				// Close tool block if open
				if toolBlockOpen {
					_ = writer.WriteContentBlockStop()
					toolBlockOpen = false
				}
				if chunk.FinishReason == "length" {
					truncated = true
				}
			}
		}

		// Close any tool block left open (e.g., stream ended without finish_reason)
		if toolBlockOpen {
			_ = writer.WriteContentBlockStop()
			toolBlockOpen = false
		}

		if streamFailed {
			break
		}

		shouldContinue := truncated && !hasToolUse && continuation.ShouldAutoContinue(fullOutput, req.Messages)
		if shouldContinue && continueCount < continuation.MaxContinuations {
			continueCount++
			h.logger.Info("auto-continuing (anthropic)", zap.Int("round", continueCount))
			req.Messages = continuation.BuildContinuationMessages(req.Messages, fullOutput)
			continue
		}
		outputTruncated = truncated && shouldContinue

		break
	}

	// Match Kiro Bridge behavior for thinking-only streams. Anthropic clients
	// expect a visible content block when no tool call is emitted.
	if thinkingOutput != "" && !emittedMeaningfulText && !hasToolUse {
		if thinkingStarted && !thinkingClosed {
			if err := writer.WriteContentBlockStop(); err != nil {
				return
			}
			thinkingStarted = false
			thinkingClosed = false
		}
		if !textStarted {
			if err := writer.WriteContentBlockStart(); err != nil {
				return
			}
			textStarted = true
		}
		if err := writer.WriteContentDelta(" "); err != nil {
			return
		}
		outputTruncated = true
	}

	// Close text block if still open
	if textStarted && !textClosed {
		_ = writer.WriteContentBlockStop()
	}
	if thinkingStarted && !thinkingClosed {
		_ = writer.WriteContentBlockStop()
	}

	stopReason := "end_turn"
	if hasToolUse {
		stopReason = "tool_use"
	} else if outputTruncated {
		stopReason = "max_tokens"
	} else if thinkingOutput != "" && !emittedMeaningfulText {
		stopReason = "max_tokens"
	}
	outputTokens := len(fullOutput) / 4
	recordUsage(h.store, c, req.Model, provider.Name(), inputTokens, outputTokens, start)
	_ = writer.WriteMessageDelta(stopReason, outputTokens)
	_ = writer.WriteMessageStop()
}

// CountTokens handles /v1/messages/count_tokens.
// Estimates token count from message content using a character-based heuristic
// (~4 chars per token for English text, consistent with BPE tokenizer averages).
func (h *AnthropicHandler) CountTokens(c *gin.Context) {
	var req struct {
		Model    string                    `json:"model"`
		System   json.RawMessage           `json:"system,omitempty"`
		Messages []models.AnthropicMessage `json:"messages"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":  "error",
			"error": gin.H{"type": "invalid_request_error", "message": err.Error()},
		})
		return
	}
	if _, ok := middleware.ResolveKiroAccount(c, c.Param("kiro_account")); !ok {
		return
	}
	req.Model = normalizeGatewayModelID(req.Model)
	if !middleware.CheckModelPermission(c, req.Model) {
		return
	}

	totalChars := len(string(req.System))
	for _, msg := range req.Messages {
		totalChars += len(string(msg.Content))
	}

	// ~4 chars per token is a reasonable BPE approximation.
	// Add a small overhead for message framing (role tokens, etc.).
	estimated := totalChars/4 + len(req.Messages)*4

	c.JSON(http.StatusOK, gin.H{
		"input_tokens": estimated,
	})
}

func ensureAnthropicToolUseID(id string) string {
	if id != "" {
		return id
	}
	return "toolu_" + uuid.New().String()[:24]
}
