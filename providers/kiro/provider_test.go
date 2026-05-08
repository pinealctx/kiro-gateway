package kiro

import (
	"strings"
	"testing"
	"time"

	"github.com/pinealctx/kiro-gateway/models"
	"go.uber.org/zap"
)

func TestApplyPayloadGuardRejectsOversizedPayload(t *testing.T) {
	restore := runtimeConfig
	defer func() { runtimeConfig = restore }()
	ConfigureRuntime(RuntimeConfig{
		FirstTokenTimeout: time.Second,
		FirstTokenRetries: 0,
		MaxPayloadBytes:   200,
		AutoTrimPayload:   false,
	})

	p := &Provider{logger: zap.NewNop()}
	err := p.applyPayloadGuard(payloadGuardRequest(4, strings.Repeat("x", 200)))
	if err == nil {
		t.Fatal("expected oversized payload error")
	}
	if !strings.Contains(err.Error(), "kiro payload is too large") {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyPayloadGuardTrimsOldestHistory(t *testing.T) {
	restore := runtimeConfig
	defer func() { runtimeConfig = restore }()
	ConfigureRuntime(RuntimeConfig{
		FirstTokenTimeout: time.Second,
		FirstTokenRetries: 0,
		MaxPayloadBytes:   900,
		AutoTrimPayload:   true,
	})

	cwReq := payloadGuardRequest(8, strings.Repeat("x", 120))
	originalHistory := len(cwReq.ConversationState.History)
	p := &Provider{logger: zap.NewNop()}

	if err := p.applyPayloadGuard(cwReq); err != nil {
		t.Fatalf("applyPayloadGuard: %v", err)
	}
	if len(cwReq.ConversationState.History) >= originalHistory {
		t.Fatalf("history was not trimmed: got %d, original %d", len(cwReq.ConversationState.History), originalHistory)
	}
	if len(cwReq.ConversationState.History) < 2 {
		t.Fatalf("system pair should be preserved, history len = %d", len(cwReq.ConversationState.History))
	}
	if size := cwPayloadSize(cwReq); size > runtimeConfig.MaxPayloadBytes {
		t.Fatalf("payload size = %d, want <= %d", size, runtimeConfig.MaxPayloadBytes)
	}
}

func payloadGuardRequest(historyEntries int, content string) *models.CWRequest {
	history := make([]models.CWHistoryEntry, 0, historyEntries+2)
	for i := 0; i < historyEntries; i++ {
		history = append(history, models.CWHistoryEntry{
			UserInputMessage: &models.CWUserInputMessage{
				Content: content,
				ModelID: "claude-sonnet-4.5",
				Origin:  "AI_EDITOR",
			},
		})
	}
	return &models.CWRequest{
		ConversationState: models.CWConversationState{
			ChatTriggerType: "MANUAL",
			ConversationID:  "test",
			CurrentMessage: models.CWCurrentMsg{
				UserInputMessage: models.CWUserInputMessage{
					Content: "current",
					ModelID: "claude-sonnet-4.5",
					Origin:  "AI_EDITOR",
				},
			},
			History: history,
		},
	}
}
