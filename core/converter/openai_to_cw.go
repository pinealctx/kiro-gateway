package converter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/pinealctx/kiro-gateway/core/sanitizer"
	"github.com/pinealctx/kiro-gateway/core/thinking"
	"github.com/pinealctx/kiro-gateway/models"
)

var kiroModelMap = map[string]string{
	"claude-opus-4-7":    "claude-opus-4.7",
	"claude-opus-4.7":    "claude-opus-4.7",
	"claude-opus-4-7-1m": "claude-opus-4.7",
	"claude-opus-4.7-1m": "claude-opus-4.7",

	"claude-opus-4-6-1m": "claude-opus-4.6",
	"claude-opus-4-6.1m": "claude-opus-4.6",
	"claude-opus-4.6-1m": "claude-opus-4.6",
	"claude-opus-4-6":    "claude-opus-4.6",
	"claude-opus-4.6":    "claude-opus-4.6",
}

// ResolveModel applies the Kiro model aliases observed in kiro-bridge-go.
// Behavior:
//  1. trim spaces
//  2. map known Kiro aliases / unsupported 1m variants to backend model ids
//  3. normalize Claude date suffix (claude-*-YYYYMMDD -> claude-*)
//  4. pass-through for all other values
//  5. empty model remains empty; callers own model selection
func ResolveModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if mapped, ok := kiroModelMap[model]; ok {
		return mapped
	}
	if strings.HasPrefix(model, "claude-") {
		parts := strings.Split(model, "-")
		if n := len(parts); n >= 4 {
			last := parts[n-1]
			if len(last) == 8 && last[0] >= '2' && last[0] <= '9' {
				return strings.Join(parts[:n-1], "-")
			}
		}
	}
	return model
}

// OpenAIToCW converts an OpenAI chat completion request to CodeWhisperer format.
func OpenAIToCW(req *models.ChatCompletionRequest, profileArn string) (*models.CWRequest, error) {
	modelID := ResolveModel(req.Model)
	convID := uuid.New().String()

	// 1. Extract system messages
	systemParts := []string{}
	nonSystemMsgs := []models.ChatMessage{}
	for _, msg := range req.Messages {
		if msg.Role == "system" || msg.Role == "developer" {
			systemParts = append(systemParts, contentToString(msg.Content))
		} else {
			nonSystemMsgs = append(nonSystemMsgs, msg)
		}
	}

	hasTools := len(req.Tools) > 0
	userSystem := thinking.InjectHint(strings.Join(systemParts, "\n"), thinking.ParseConfig(req.Extras))
	systemPrompt := sanitizer.BuildSystemPrompt(userSystem, hasTools)

	// 2. Convert tools
	cwTools := convertTools(req.Tools)

	// 3. Build history: first inject system prompt as a user/assistant pair
	history := []models.CWHistoryEntry{}

	// System injection pair
	history = append(history, models.CWHistoryEntry{
		UserInputMessage: &models.CWUserInputMessage{
			Content: systemPrompt,
			ModelID: modelID,
			Origin:  "AI_EDITOR",
		},
	})
	history = append(history, models.CWHistoryEntry{
		AssistantResponseMessage: &models.CWAssistantResponseMessage{
			MessageID: uuid.New().String(),
			Content:   "Understood. I am Claude, made by Anthropic. I will follow the instructions provided.",
		},
	})

	// 4. Convert message history (all but the tail)
	if len(nonSystemMsgs) == 0 {
		return nil, fmt.Errorf("no non-system messages provided")
	}

	// Find the tail boundary:
	// If trailing messages are tool role, they form the current toolResults
	// Otherwise the last user message is the current message
	toolResultBoundary := len(nonSystemMsgs)
	for i := len(nonSystemMsgs) - 1; i >= 0; i-- {
		if nonSystemMsgs[i].Role == "tool" {
			toolResultBoundary = i
		} else {
			break
		}
	}

	var histMsgs []models.ChatMessage
	var tailMsgs []models.ChatMessage
	if toolResultBoundary < len(nonSystemMsgs) {
		// Tool result mode: history is everything before the trailing tools
		histMsgs = nonSystemMsgs[:toolResultBoundary]
		tailMsgs = nonSystemMsgs[toolResultBoundary:]
	} else {
		// Normal mode: history is everything except the last message
		if len(nonSystemMsgs) > 1 {
			histMsgs = nonSystemMsgs[:len(nonSystemMsgs)-1]
		}
		tailMsgs = nonSystemMsgs[len(nonSystemMsgs)-1:]
	}

	// Build paired history: buffer user/tool messages, flush on assistant
	var pendingMsgs []models.ChatMessage
	for _, msg := range histMsgs {
		switch msg.Role {
		case "user", "tool":
			pendingMsgs = append(pendingMsgs, msg)
		case "assistant":
			if len(pendingMsgs) > 0 {
				history = append(history, buildHistoryUserEntry(pendingMsgs, modelID))
				pendingMsgs = nil
			}
			entry := models.CWHistoryEntry{
				AssistantResponseMessage: &models.CWAssistantResponseMessage{
					MessageID: uuid.New().String(),
					Content:   contentToString(msg.Content),
				},
			}
			if len(msg.ToolCalls) > 0 {
				toolUses := make([]models.CWToolUse, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					var input any = map[string]any{}
					if tc.Function.Arguments != "" {
						if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
							input = map[string]any{}
						}
					}
					toolUses = append(toolUses, models.CWToolUse{
						ToolUseID: tc.ID,
						Name:      tc.Function.Name,
						Input:     input,
					})
				}
				entry.AssistantResponseMessage.ToolUses = toolUses
			}
			history = append(history, entry)
		}
	}
	// Flush remaining user buffer with a synthetic assistant reply
	if len(pendingMsgs) > 0 {
		history = append(history, buildHistoryUserEntry(pendingMsgs, modelID))
		history = append(history, models.CWHistoryEntry{
			AssistantResponseMessage: &models.CWAssistantResponseMessage{
				MessageID: uuid.New().String(),
				Content:   "OK",
			},
		})
	}

	// 5. Build current message from tail
	currentContent := ""
	var toolResults []models.CWToolResult
	var images []models.CWImage

	for _, msg := range tailMsgs {
		switch msg.Role {
		case "user":
			currentContent = contentToString(msg.Content)
			if imgs := extractImages(msg.Content); len(imgs) > 0 {
				images = append(images, imgs...)
			}
		case "tool":
			text := toolResultText(msg.Content)
			if len(text) > 50000 {
				text = text[:50000]
			}
			toolResults = append(toolResults, models.CWToolResult{
				ToolUseID: msg.ToolCallID,
				Content:   []models.CWToolResultContent{{Text: text}},
				Status:    "success",
			})
		}
	}

	cwReq := &models.CWRequest{
		ConversationState: models.CWConversationState{
			ChatTriggerType: "MANUAL",
			ConversationID:  convID,
			CurrentMessage: models.CWCurrentMsg{
				UserInputMessage: models.CWUserInputMessage{
					Content: currentContent,
					ModelID: modelID,
					Origin:  "AI_EDITOR",
				},
			},
			History: history,
		},
		ProfileArn: profileArn,
	}

	// Always attach tools + toolResults to the current message context
	// when either is present. CW requires tools to be sent alongside toolResults.
	if len(cwTools) > 0 || len(toolResults) > 0 {
		cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext = &models.CWMessageContext{
			Tools:       cwTools,
			ToolResults: toolResults,
		}
	}

	if len(images) > 0 {
		cwReq.ConversationState.CurrentMessage.UserInputMessage.Images = images
	}

	return cwReq, nil
}

// convertTools converts OpenAI tool definitions to CW format.
func convertTools(tools []models.Tool) []models.CWTool {
	cwTools := make([]models.CWTool, 0, len(tools))
	for _, t := range tools {
		name := t.Function.Name
		// Filter out web_search / websearch
		lower := strings.ToLower(name)
		if lower == "web_search" || lower == "websearch" {
			continue
		}
		desc := t.Function.Description
		if len(desc) > 10000 {
			desc = desc[:10000]
		}
		var params any = t.Function.Parameters
		if len(t.Function.Parameters) == 0 || string(t.Function.Parameters) == "null" {
			params = map[string]any{}
		}
		cwTools = append(cwTools, models.CWTool{
			ToolSpecification: models.CWToolSpec{
				Name:        name,
				Description: desc,
				InputSchema: models.CWInputSchema{JSON: params},
			},
		})
	}
	return cwTools
}

// buildHistoryUserEntry groups user and tool messages into a single CW history entry.
func buildHistoryUserEntry(msgs []models.ChatMessage, modelID string) models.CWHistoryEntry {
	var texts []string
	var toolResults []models.CWToolResult
	var images []models.CWImage

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			if t := contentToString(msg.Content); t != "" {
				texts = append(texts, t)
			}
			if imgs := extractImages(msg.Content); len(imgs) > 0 {
				images = append(images, imgs...)
			}
		case "tool":
			text := toolResultText(msg.Content)
			if len(text) > 50000 {
				text = text[:50000]
			}
			toolResults = append(toolResults, models.CWToolResult{
				ToolUseID: msg.ToolCallID,
				Content:   []models.CWToolResultContent{{Text: text}},
				Status:    "success",
			})
		}
	}

	content := strings.Join(texts, "\n")
	// When only tool results, CW still requires a content field
	if content == "" && len(toolResults) > 0 {
		content = ""
	}

	entry := models.CWHistoryEntry{
		UserInputMessage: &models.CWUserInputMessage{
			Content: content,
			ModelID: modelID,
			Origin:  "AI_EDITOR",
		},
	}
	if len(images) > 0 {
		entry.UserInputMessage.Images = images
	}
	if len(toolResults) > 0 {
		entry.UserInputMessage.UserInputMessageContext = &models.CWMessageContext{
			ToolResults: toolResults,
		}
	}
	return entry
}

// contentToString extracts text content from a ChatMessage.Content (json.RawMessage).
func contentToString(content json.RawMessage) string {
	return models.ContentText(content)
}

func toolResultText(content json.RawMessage) string {
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return parseToolResultString(s)
	}

	var parts []any
	if err := json.Unmarshal(content, &parts); err == nil {
		texts := make([]string, 0, len(parts))
		for _, item := range parts {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					texts = append(texts, text)
					continue
				}
			}
			b, _ := json.Marshal(item)
			texts = append(texts, string(b))
		}
		return strings.Join(texts, "\n")
	}

	return contentToString(content)
}

func parseToolResultString(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") {
		return text
	}
	var parts []any
	if err := json.Unmarshal([]byte(trimmed), &parts); err != nil {
		return text
	}
	texts := make([]string, 0, len(parts))
	for _, item := range parts {
		if m, ok := item.(map[string]any); ok {
			if t, ok := m["text"].(string); ok {
				texts = append(texts, t)
				continue
			}
		}
		b, _ := json.Marshal(item)
		texts = append(texts, string(b))
	}
	return strings.Join(texts, "\n")
}

// extractImages pulls image data from content parts and converts to CW format.
func extractImages(content json.RawMessage) []models.CWImage {
	parts, ok := models.ContentParts(content)
	if !ok {
		return nil
	}
	var images []models.CWImage
	for _, m := range parts {
		partType, _ := m["type"].(string)

		switch partType {
		case "image_url":
			// OpenAI format: data:image/png;base64,...
			if imgURL, ok := m["image_url"].(map[string]any); ok {
				if url, ok := imgURL["url"].(string); ok {
					if format, data, ok := parseDataURI(url); ok {
						images = append(images, models.CWImage{
							Format: format,
							Source: models.CWImageSource{Bytes: data},
						})
					}
				}
			}
		case "image":
			// Anthropic format
			if src, ok := m["source"].(map[string]any); ok {
				mediaType, _ := src["media_type"].(string)
				data, _ := src["data"].(string)
				format := "png"
				if strings.Contains(mediaType, "jpeg") || strings.Contains(mediaType, "jpg") {
					format = "jpg"
				}
				images = append(images, models.CWImage{
					Format: format,
					Source: models.CWImageSource{Bytes: data},
				})
			}
		}
	}
	return images
}

// parseDataURI extracts format and base64 data from a data URI.
func parseDataURI(uri string) (format string, data string, ok bool) {
	// data:image/png;base64,iVBOR...
	if !strings.HasPrefix(uri, "data:image/") {
		return "", "", false
	}
	rest := strings.TrimPrefix(uri, "data:image/")
	parts := strings.SplitN(rest, ";base64,", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	format = parts[0]
	if format == "jpeg" {
		format = "jpg"
	}
	return format, parts[1], true
}
