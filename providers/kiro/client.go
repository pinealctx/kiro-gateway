package kiro

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"time"

	"github.com/pinealctx/kiro-gateway/core/eventstream"
	"github.com/pinealctx/kiro-gateway/core/logutil"
	"github.com/pinealctx/kiro-gateway/models"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	cwTarget = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"

	maxRetries = 3
	maxLogBody = 8 * 1024

	// Kiro's Q runtime endpoint is not available in every AWS/IDC region.
	// Account regions are still used for login/token flows; API calls go to
	// the fixed Kiro service region.
	kiroAPIRegion = "us-east-1"
)

// retryBackoff defines the backoff durations for retries: [1s, 3s, 10s].
var retryBackoff = []time.Duration{1 * time.Second, 3 * time.Second, 10 * time.Second}

// CWClient handles HTTP communication with the CodeWhisperer backend.
type CWClient struct {
	client      *http.Client
	logger      *zap.Logger
	apiRegion   string
	fingerprint string
}

func NewCWClient(logger *zap.Logger, _ string) *CWClient {
	return &CWClient{
		client: &http.Client{
			// Long read timeout for Claude Code sessions that can produce very long outputs.
			// Connect/TLS handshake is bounded by the OS default (~30s).
			Timeout: 2 * time.Hour,
		},
		logger:      logger,
		apiRegion:   kiroAPIRegion,
		fingerprint: machineFingerprint(),
	}
}

// CWStreamEvent represents a parsed event from the CW response stream.
type CWStreamEvent struct {
	Type         string // "text", "tool_use", "context_usage", "exception", "end"
	Content      string
	Reasoning    string
	ToolUse      *CWToolUseAccumulator
	ContextUsage float64
	Error        error
}

type CWToolUseAccumulator struct {
	ToolUseID string
	Name      string
	Input     any
}

// GenerateStream sends a request to CW and returns an event channel.
// Retries up to maxRetries times on 5xx errors or timeouts with exponential backoff.
func (c *CWClient) GenerateStream(ctx context.Context, cwReq *models.CWRequest, token *TokenInfo) (<-chan CWStreamEvent, error) {
	bodyBytes, err := json.Marshal(cwReq)
	if err != nil {
		return nil, fmt.Errorf("marshal cw request: %w", err)
	}

	debugEnabled := c.logger.Core().Enabled(zapcore.DebugLevel)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryBackoff[attempt-1]
			c.logger.Warn("retrying CW request",
				zap.Int("attempt", attempt),
				zap.Duration("backoff", delay),
				zap.Error(lastErr),
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint("generateAssistantResponse"), bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
		req.Header.Set("x-amz-target", cwTarget)
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		req.Header.Set("User-Agent", "kiro-cli-chat-macos-aarch64-1.27.2")
		req.Header.Set("x-amzn-codewhisperer-optout", "true")
		if token.IsExternalIdP {
			req.Header.Set("TokenType", "EXTERNAL_IDP")
		}
		debugKiroHTTPRequest(c.logger, "kiro upstream request", req, bodyBytes)

		resp, err := c.client.Do(req)
		if err != nil {
			// Don't retry on context cancellation — client disconnected
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("cw request failed: %w", err)
			continue // retry on network/timeout errors
		}

		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if debugEnabled {
				respBody, truncated := logutil.TruncateString(logutil.RedactString(string(body)), maxLogBody)
				respBody = logutil.WithTruncationSuffix(respBody, truncated, len(body), maxLogBody)
				c.logger.Debug("kiro upstream response",
					zap.Int("status", resp.StatusCode),
					zap.Any("headers", logutil.RedactHeaders(resp.Header)),
					zap.String("response_body", respBody),
				)
			}
			lastErr = fmt.Errorf("cw returned %d: %s", resp.StatusCode, string(body))
			continue // retry on 5xx
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if debugEnabled {
				respBody, truncated := logutil.TruncateString(logutil.RedactString(string(body)), maxLogBody)
				respBody = logutil.WithTruncationSuffix(respBody, truncated, len(body), maxLogBody)
				c.logger.Debug("kiro upstream response",
					zap.Int("status", resp.StatusCode),
					zap.Any("headers", logutil.RedactHeaders(resp.Header)),
					zap.String("response_body", respBody),
				)
			}
			return nil, fmt.Errorf("cw returned %d: %s", resp.StatusCode, string(body))
		}

		if debugEnabled {
			c.logger.Debug("kiro upstream response",
				zap.Int("status", resp.StatusCode),
				zap.Any("headers", logutil.RedactHeaders(resp.Header)),
				zap.String("response_body", "[streaming event payloads logged separately]"),
			)
		}

		events := make(chan CWStreamEvent, 32)
		go c.processStream(resp.Body, events)
		return events, nil
	}

	return nil, fmt.Errorf("cw request failed after %d retries: %w", maxRetries, lastErr)
}

func (c *CWClient) endpoint(path string) string {
	return fmt.Sprintf("https://q.%s.amazonaws.com/%s", normalizeRegion(c.apiRegion), path)
}

func (c *CWClient) userAgent() string {
	return "aws-sdk-js/1.0.27 ua/2.1 os/win32#10.0.19044 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.7.45-" + c.fingerprint
}

func (c *CWClient) processStream(body io.ReadCloser, out chan<- CWStreamEvent) {
	defer close(out)
	defer func() { _ = body.Close() }()

	rawEvents := make(chan eventstream.Event, 32)
	go func() {
		if err := eventstream.ParseStreamingResponse(body, rawEvents); err != nil {
			c.logger.Error("eventstream parse error", zap.Error(err))
		}
	}()

	var activeTool *struct {
		ID       string
		Name     string
		InputBuf string
	}

	for raw := range rawEvents {
		if c.logger.Core().Enabled(zapcore.DebugLevel) && shouldLogCWEvent(raw.EventType) {
			payload := logutil.RedactString(string(raw.Payload))
			payload, truncated := logutil.TruncateString(payload, maxLogBody)
			payload = logutil.WithTruncationSuffix(payload, truncated, len(raw.Payload), maxLogBody)
			c.logger.Debug("kiro upstream stream event raw",
				zap.String("message_type", raw.MessageType),
				zap.String("event_type", raw.EventType),
				zap.String("payload", payload),
			)
		}

		if raw.MessageType == "exception" {
			var exc models.CWExceptionEvent
			_ = json.Unmarshal(raw.Payload, &exc)
			out <- CWStreamEvent{Type: "exception", Error: fmt.Errorf("CW exception: %s", exc.Message)}
			continue
		}

		switch raw.EventType {
		case "assistantResponseEvent":
			var evt models.CWAssistantResponseEvent
			if err := json.Unmarshal(raw.Payload, &evt); err == nil {
				out <- CWStreamEvent{Type: "text", Content: evt.Content}
			}

		case "reasoningContentEvent":
			var evt struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw.Payload, &evt); err == nil && evt.Text != "" {
				out <- CWStreamEvent{Type: "reasoning", Reasoning: evt.Text}
			}

		case "toolUse":
			// Legacy: complete tool use in one event
			var evt struct {
				ToolUseID string `json:"toolUseId"`
				Name      string `json:"name"`
				Input     any    `json:"input"`
			}
			if err := json.Unmarshal(raw.Payload, &evt); err == nil {
				out <- CWStreamEvent{
					Type: "tool_use",
					ToolUse: &CWToolUseAccumulator{
						ToolUseID: evt.ToolUseID,
						Name:      evt.Name,
						Input:     evt.Input,
					},
				}
			}

		case "toolUseEvent":
			// Streaming tool use: accumulate chunks using dynamic JSON parsing
			var payload map[string]any
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				c.logger.Warn("failed to parse toolUseEvent payload", zap.Error(err))
				continue
			}

			name, _ := payload["name"].(string)
			toolUseID, _ := payload["toolUseId"].(string)
			isStop, _ := payload["stop"].(bool)

			if name != "" && toolUseID != "" && activeTool == nil {
				activeTool = &struct {
					ID       string
					Name     string
					InputBuf string
				}{ID: toolUseID, Name: name}
			}
			if inputStr, ok := payload["input"].(string); ok && inputStr != "" && activeTool != nil {
				activeTool.InputBuf += inputStr
			}
			if isStop && activeTool != nil {
				var input any = map[string]any{}
				if activeTool.InputBuf != "" {
					if err := json.Unmarshal([]byte(activeTool.InputBuf), &input); err != nil {
						c.logger.Warn("failed to parse accumulated tool input JSON, using raw fallback",
							zap.String("tool", activeTool.Name),
							zap.Int("buf_len", len(activeTool.InputBuf)),
							zap.Error(err))
						input = map[string]any{"raw": activeTool.InputBuf}
					}
				}
				out <- CWStreamEvent{
					Type: "tool_use",
					ToolUse: &CWToolUseAccumulator{
						ToolUseID: activeTool.ID,
						Name:      activeTool.Name,
						Input:     input,
					},
				}
				activeTool = nil
			}

		case "contextUsageEvent":
			var evt models.CWContextUsageEvent
			if err := json.Unmarshal(raw.Payload, &evt); err == nil {
				out <- CWStreamEvent{Type: "context_usage", ContextUsage: evt.ContextUsagePercentage}
			}

		case "supplementaryWebLinksEvent", "meteringEvent":
			// Ignored

		default:
			// Match bridge behavior: tolerate forward-compatible CW events.
		}
	}
	if activeTool != nil {
		c.emitActiveTool(out, activeTool)
	}

	out <- CWStreamEvent{Type: "end"}
}

func shouldLogCWEvent(eventType string) bool {
	switch eventType {
	case "assistantResponseEvent", "reasoningContentEvent", "contextUsageEvent", "meteringEvent":
		return false
	default:
		return true
	}
}

func (c *CWClient) emitActiveTool(out chan<- CWStreamEvent, activeTool *struct {
	ID       string
	Name     string
	InputBuf string
}) {
	var input any = map[string]any{}
	if activeTool.InputBuf != "" {
		if err := json.Unmarshal([]byte(activeTool.InputBuf), &input); err != nil {
			c.logger.Warn("failed to parse accumulated tool input JSON, using raw fallback",
				zap.String("tool", activeTool.Name),
				zap.Int("buf_len", len(activeTool.InputBuf)),
				zap.Error(err))
			input = map[string]any{"raw": activeTool.InputBuf}
		}
	}
	out <- CWStreamEvent{
		Type: "tool_use",
		ToolUse: &CWToolUseAccumulator{
			ToolUseID: activeTool.ID,
			Name:      activeTool.Name,
			Input:     input,
		},
	}
}

func machineFingerprint() string {
	hostname, _ := os.Hostname()
	username := ""
	if u, err := user.Current(); err == nil && u != nil {
		username = u.Username
	}
	sum := sha256.Sum256([]byte(hostname + "-" + username + "-kiro-gateway"))
	return fmt.Sprintf("%x", sum[:])
}
