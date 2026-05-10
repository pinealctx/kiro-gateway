package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pinealctx/kiro-gateway/api/routes"
	"github.com/pinealctx/kiro-gateway/core/providers"
	"github.com/pinealctx/kiro-gateway/models"
	"github.com/pinealctx/kiro-gateway/tenant"
	"go.uber.org/zap"
)

// ============================================================
// Mock provider
// ============================================================

type mockProvider struct {
	name        string
	response    *models.ChatCompletionResponse
	chunks      []providers.StreamChunk
	err         error
	healthy     bool
	models      []string
	lastRequest *models.ChatCompletionRequest
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) ChatCompletion(_ context.Context, req *models.ChatCompletionRequest) (*models.ChatCompletionResponse, error) {
	m.lastRequest = req
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}
func (m *mockProvider) StreamCompletion(_ context.Context, req *models.ChatCompletionRequest, stream chan<- providers.StreamChunk) error {
	defer close(stream)
	m.lastRequest = req
	for _, c := range m.chunks {
		stream <- c
	}
	return nil
}
func (m *mockProvider) RefreshToken(_ context.Context) error { return nil }
func (m *mockProvider) IsHealthy(_ context.Context) bool     { return m.healthy }
func (m *mockProvider) ListModels(_ context.Context) ([]string, error) {
	if len(m.models) > 0 {
		return m.models, nil
	}
	return []string{"claude-sonnet-4.6"}, nil
}

// ============================================================
// Helper to create a test router with mock provider
// ============================================================

var testToken string

func setupRouter(mock *mockProvider, apiKey string) http.Handler {
	logger, _ := zap.NewDevelopment()
	if !mock.healthy {
		mock.healthy = true
	}
	registry := providers.NewRegistry()
	registry.Register(mock)
	dir, err := os.MkdirTemp("", "kiro-gateway-test-*")
	if err != nil {
		panic(err)
	}
	store, err := tenant.NewStore(filepath.Join(dir, "tenant.db"))
	if err != nil {
		panic(err)
	}
	key, err := store.CreateKey("test-key", tenant.WithKiroAccounts([]string{"kiro"}))
	if err != nil {
		panic(err)
	}
	testToken = key.Key
	return routes.SetupRouter(routes.RouterConfig{
		Registry: registry,
		Logger:   logger,
		Store:    store,
	})
}

func setupRouterWithKeyOptions(t *testing.T, mock *mockProvider, opts ...tenant.KeyOption) (http.Handler, *tenant.Store, *tenant.APIKey) {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	if !mock.healthy {
		mock.healthy = true
	}
	registry := providers.NewRegistry()
	registry.Register(mock)
	store, err := tenant.NewStore(filepath.Join(t.TempDir(), "tenant.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	keyOpts := append([]tenant.KeyOption{tenant.WithKiroAccounts([]string{"kiro"})}, opts...)
	key, err := store.CreateKey("test-key", keyOpts...)
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	testToken = key.Key
	router := routes.SetupRouter(routes.RouterConfig{
		Registry: registry,
		Logger:   logger,
		Store:    store,
	})
	return router, store, key
}

func setupRouterWithTwoKiroAccounts(t *testing.T, keyAccounts []string) (http.Handler, *tenant.Store, *mockProvider, *mockProvider) {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	kiroA := &mockProvider{
		name:    "kiro-a",
		healthy: true,
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{Message: models.ChatMessage{Role: "assistant", Content: models.RawString("a")}}},
		},
	}
	kiroB := &mockProvider{
		name:    "kiro-b",
		healthy: true,
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{Message: models.ChatMessage{Role: "assistant", Content: models.RawString("b")}}},
		},
	}
	registry := providers.NewRegistry()
	registry.Register(kiroA)
	registry.Register(kiroB)
	store, err := tenant.NewStore(filepath.Join(t.TempDir(), "tenant.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	key, err := store.CreateKey("test-key", tenant.WithKiroAccounts(keyAccounts))
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	testToken = key.Key
	router := routes.SetupRouter(routes.RouterConfig{
		Registry: registry,
		Logger:   logger,
		Store:    store,
	})
	return router, store, kiroA, kiroB
}

func doJSON(handler http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if headers == nil && testToken != "" {
		req.Header.Set("Authorization", "Bearer "+testToken)
	} else {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// ============================================================
// Root & Health
// ============================================================

func TestRoot_ReturnsServiceInfo(t *testing.T) {
	mock := &mockProvider{name: "kiro", healthy: true}
	router := setupRouter(mock, "")

	w := doJSON(router, "GET", "/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["name"] != "Kiro Gateway" {
		t.Errorf("name = %v", body["name"])
	}
}

func TestHealth_ReturnsOK(t *testing.T) {
	mock := &mockProvider{name: "kiro", healthy: true}
	router := setupRouter(mock, "")

	w := doJSON(router, "GET", "/health", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
	if body["version"] == nil {
		t.Errorf("version should be present")
	}
}

// ============================================================
// Auth middleware
// ============================================================

func TestAuth_MissingKey_Returns401(t *testing.T) {
	mock := &mockProvider{name: "kiro", healthy: true}
	router := setupRouter(mock, "test-key")

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, map[string]string{})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuth_InvalidKey_Returns401(t *testing.T) {
	mock := &mockProvider{name: "kiro", healthy: true}
	router := setupRouter(mock, "test-key")

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]string{}, map[string]string{
		"Authorization": "Bearer wrong-key",
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuth_ValidBearer_Passes(t *testing.T) {
	mock := &mockProvider{
		name:    "kiro",
		healthy: true,
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{
				Message:      models.ChatMessage{Role: "assistant", Content: models.RawString("hello")},
				FinishReason: "stop",
			}},
		},
	}
	router := setupRouter(mock, "test-key")

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, map[string]string{"Authorization": "Bearer " + testToken})
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
}

func TestAuth_XAPIKey_Passes(t *testing.T) {
	mock := &mockProvider{
		name:    "kiro",
		healthy: true,
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{
				Message:      models.ChatMessage{Role: "assistant", Content: models.RawString("hello")},
				FinishReason: "stop",
			}},
		},
	}
	router := setupRouter(mock, "test-key")

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, map[string]string{"x-api-key": testToken})
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestAuth_AlwaysRequiresKey(t *testing.T) {
	mock := &mockProvider{
		name:    "kiro",
		healthy: true,
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{
				Message:      models.ChatMessage{Role: "assistant", Content: models.RawString("ok")},
				FinishReason: "stop",
			}},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "gpt-4",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, map[string]string{})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestRuntime_URLAccount_SelectsAllowedKiroAccount(t *testing.T) {
	router, store, kiroA, kiroB := setupRouterWithTwoKiroAccounts(t, []string{"kiro-a", "kiro-b"})
	defer store.Close()

	w := doJSON(router, "POST", "/a/kiro-b/v1/chat/completions", map[string]any{
		"model":    "claude-opus-4.7",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if kiroB.lastRequest == nil {
		t.Fatal("expected kiro-b to receive request")
	}
	if kiroA.lastRequest != nil {
		t.Fatal("kiro-a should not receive URL-selected request")
	}
}

func TestRuntime_URLAccount_RejectsDisallowedKiroAccount(t *testing.T) {
	router, store, _, kiroB := setupRouterWithTwoKiroAccounts(t, []string{"kiro-a"})
	defer store.Close()

	w := doJSON(router, "POST", "/a/kiro-b/v1/chat/completions", map[string]any{
		"model":    "claude-opus-4.7",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", w.Code, w.Body.String())
	}
	if kiroB.lastRequest != nil {
		t.Fatal("disallowed account should not receive request")
	}
}

func TestRuntime_PlainV1RouteUsesAPIKeyDefaultKiroAccount(t *testing.T) {
	router, store, kiroA, kiroB := setupRouterWithTwoKiroAccounts(t, []string{"kiro-a", "kiro-b"})
	defer store.Close()

	w := doJSON(router, "POST", "/v1/chat/completions", map[string]any{
		"model":    "claude-opus-4.7",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if kiroA.lastRequest == nil {
		t.Fatal("plain /v1 route should use default account kiro-a")
	}
	if kiroB.lastRequest != nil {
		t.Fatal("plain /v1 route should not reach non-default account")
	}
}

func TestRuntime_PlainV1RouteUsesConfiguredDefaultKiroAccount(t *testing.T) {
	router, store, kiroA, kiroB := setupRouterWithTwoKiroAccounts(t, []string{"kiro-a", "kiro-b"})
	defer store.Close()
	keys := store.ListKeys()
	if len(keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(keys))
	}
	if _, err := store.UpdateKey(keys[0].ID, tenant.WithKiroDefaultAccount("kiro-b")); err != nil {
		t.Fatalf("UpdateKey() error = %v", err)
	}

	w := doJSON(router, "POST", "/v1/chat/completions", map[string]any{
		"model":    "claude-opus-4.7",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if kiroB.lastRequest == nil {
		t.Fatal("plain /v1 route should use configured default account kiro-b")
	}
	if kiroA.lastRequest != nil {
		t.Fatal("plain /v1 route should not reach non-default account")
	}
}

// ============================================================
// OpenAI endpoints
// ============================================================

func TestOpenAI_ChatCompletions_NonStream(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		response: &models.ChatCompletionResponse{
			ID:    "test-123",
			Model: "claude-opus-4.6",
			Choices: []models.ChatCompletionChoice{{
				Index:        0,
				Message:      models.ChatMessage{Role: "assistant", Content: models.RawString("Hello there!")},
				FinishReason: "stop",
			}},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "claude-sonnet-4-20250514",
		"messages": []map[string]string{{"role": "user", "content": "say hi"}},
	}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	var resp models.ChatCompletionResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q", resp.Choices[0].FinishReason)
	}
}

func TestOpenAI_ChatCompletions_EmptyModelReturns400(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{Message: models.ChatMessage{Role: "assistant", Content: models.RawString("ok")}}},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "   ",
		"messages": []map[string]string{{"role": "user", "content": "say hi"}},
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	if mock.lastRequest != nil {
		t.Fatal("empty model request should not reach upstream")
	}
}

func TestOpenAI_ChatCompletions_RecordsUsage(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		response: &models.ChatCompletionResponse{
			ID:    "test-123",
			Model: "claude-opus-4.6",
			Usage: &models.Usage{
				PromptTokens:     11,
				CompletionTokens: 7,
				TotalTokens:      18,
			},
			Choices: []models.ChatCompletionChoice{{
				Index:        0,
				Message:      models.ChatMessage{Role: "assistant", Content: models.RawString("Hello")},
				FinishReason: "stop",
			}},
		},
	}
	router, store, key := setupRouterWithKeyOptions(t, mock)
	defer store.Close()

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "claude-opus-4.6",
		"messages": []map[string]string{{"role": "user", "content": "say hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	usage, err := store.QueryUsage(tenant.UsageQuery{KeyID: key.ID})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if len(usage) != 1 || usage[0].TotalRequests != 1 || usage[0].TotalTokens != 18 {
		t.Fatalf("usage = %+v, want one request with 18 tokens", usage)
	}
}

func TestOpenAI_ChatCompletions_ModelIsNotControlledByAPIKey(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{Message: models.ChatMessage{Role: "assistant", Content: models.RawString("ok")}}},
		},
	}
	router, store, _ := setupRouterWithKeyOptions(t, mock)
	defer store.Close()

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "claude-sonnet-4.6",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if mock.lastRequest == nil || mock.lastRequest.Model != "claude-sonnet-4.6" {
		t.Fatalf("model was not passed through: %+v", mock.lastRequest)
	}
}

func TestOpenAI_ChatCompletions_InvalidBody(t *testing.T) {
	mock := &mockProvider{name: "kiro"}
	router := setupRouter(mock, "")

	req := httptest.NewRequest("POST", "/a/kiro/v1/chat/completions", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestOpenAI_ChatCompletions_Stream_SSE(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		chunks: []providers.StreamChunk{
			{Content: "Hello "},
			{Content: "world!"},
			{FinishReason: "stop"},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "gpt-4",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "data:") {
		t.Errorf("expected SSE data: lines, got:\n%s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("expected [DONE] marker")
	}
}

func TestOpenAI_Models_ReturnsList(t *testing.T) {
	mock := &mockProvider{name: "kiro", models: []string{"model-a", "model-b"}}
	router := setupRouter(mock, "")

	w := doJSON(router, "GET", "/a/kiro/v1/models", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["object"] != "list" {
		t.Errorf("object = %v", body["object"])
	}
	data, ok := body["data"].([]any)
	if !ok || len(data) == 0 {
		t.Errorf("models list should not be empty")
	}
	first := data[0].(map[string]any)
	if first["id"] != "anthropic.model-a" {
		t.Errorf("first model = %v, want anthropic.model-a", first["id"])
	}
	if first["display_name"] != "model-a" {
		t.Errorf("display_name = %v, want model-a", first["display_name"])
	}
	if first["kiro_model_id"] != "model-a" {
		t.Errorf("kiro_model_id = %v, want model-a", first["kiro_model_id"])
	}
}

func TestGatewayModelAlias_NormalizesAnthropicPrefix(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{Message: models.ChatMessage{Role: "assistant", Content: models.RawString("ok")}}},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/chat/completions", map[string]any{
		"model":    "anthropic.deepseek-3.2",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if mock.lastRequest == nil || mock.lastRequest.Model != "deepseek-3.2" {
		t.Fatalf("model = %+v, want deepseek-3.2", mock.lastRequest)
	}
}

// ============================================================
// Anthropic endpoints
// ============================================================

func TestAnthropic_Messages_NonStream(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{
				Message:      models.ChatMessage{Role: "assistant", Content: models.RawString("Hi from Claude")},
				FinishReason: "stop",
			}},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages", map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"messages":   []map[string]string{{"role": "user", "content": "hello"}},
	}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	var resp models.AnthropicResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Type != "message" {
		t.Errorf("type = %q, want message", resp.Type)
	}
	if resp.Role != "assistant" {
		t.Errorf("role = %q", resp.Role)
	}
	if len(resp.Content) == 0 {
		t.Fatal("expected content blocks")
	}
	if resp.Content[0].Type != "text" {
		t.Errorf("content[0].type = %q", resp.Content[0].Type)
	}
}

func TestAnthropic_Messages_EmptyModelReturns400(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{Message: models.ChatMessage{Role: "assistant", Content: models.RawString("ok")}}},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages", map[string]any{
		"model":      "",
		"max_tokens": 1024,
		"messages":   []map[string]string{{"role": "user", "content": "hello"}},
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	if mock.lastRequest != nil {
		t.Fatal("empty model request should not reach upstream")
	}
}

func TestAnthropic_Messages_InvalidBody(t *testing.T) {
	mock := &mockProvider{name: "kiro"}
	router := setupRouter(mock, "")

	req := httptest.NewRequest("POST", "/a/kiro/v1/messages", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestAnthropic_Messages_Stream_SSE(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		chunks: []providers.StreamChunk{
			{Content: "stream "},
			{Content: "reply"},
			{FinishReason: "stop"},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages", map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "event:") {
		t.Errorf("expected Anthropic SSE event: lines, got:\n%s", body)
	}
}

func TestAnthropic_Messages_WithToolCalls(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{
				Message: models.ChatMessage{
					Role: "assistant",
					ToolCalls: []models.ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: models.ToolCallFunction{
							Name:      "get_weather",
							Arguments: `{"city":"Tokyo"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages", map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"messages":   []map[string]string{{"role": "user", "content": "weather in Tokyo?"}},
	}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	var resp models.AnthropicResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", resp.StopReason)
	}
	foundToolUse := false
	for _, block := range resp.Content {
		if block.Type == "tool_use" {
			foundToolUse = true
			if block.Name != "get_weather" {
				t.Errorf("tool name = %q", block.Name)
			}
		}
	}
	if !foundToolUse {
		t.Error("expected tool_use content block")
	}
}

func TestAnthropic_Messages_NonStreamThinking(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		response: &models.ChatCompletionResponse{
			Choices: []models.ChatCompletionChoice{{
				Message: models.ChatMessage{
					Role:             "assistant",
					ReasoningContent: "thinking text",
					Content:          models.RawString("final text"),
				},
				FinishReason: "stop",
			}},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages", map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"messages":   []map[string]string{{"role": "user", "content": "hello"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp models.AnthropicResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Content) < 2 || resp.Content[0].Type != "thinking" || resp.Content[0].Thinking != "thinking text" {
		t.Fatalf("content = %+v, want leading thinking block", resp.Content)
	}
}

func TestAnthropic_Messages_StreamThinking(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		chunks: []providers.StreamChunk{
			{ReasoningContent: "thinking "},
			{Content: "reply"},
			{FinishReason: "stop"},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages", map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"thinking"`) || !strings.Contains(body, `"type":"thinking_delta"`) {
		t.Fatalf("expected thinking events, got:\n%s", body)
	}
}

func TestAnthropic_Messages_StreamThinkingOnlyAddsPlaceholderText(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		chunks: []providers.StreamChunk{
			{ReasoningContent: "thinking only"},
			{FinishReason: "stop"},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages", map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"thinking_delta"`) {
		t.Fatalf("expected thinking delta, got:\n%s", body)
	}
	if !strings.Contains(body, `"type":"text_delta"`) || !strings.Contains(body, `"text":" "`) {
		t.Fatalf("expected placeholder text delta, got:\n%s", body)
	}
	if !strings.Contains(body, `"stop_reason":"max_tokens"`) {
		t.Fatalf("expected max_tokens stop reason, got:\n%s", body)
	}
}

func TestAnthropic_Messages_StreamLengthStopReason(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		chunks: []providers.StreamChunk{
			{Content: "partial"},
			{FinishReason: "length"},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages", map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, `"stop_reason":"max_tokens"`) {
		t.Fatalf("expected max_tokens stop reason, got:\n%s", body)
	}
}

func TestAnthropic_Messages_StreamErrorUsesAnthropicErrorEvent(t *testing.T) {
	mock := &mockProvider{
		name: "kiro",
		chunks: []providers.StreamChunk{
			{Error: errors.New("cw returned 429")},
		},
	}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages", map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, `"type":"api_error"`) {
		t.Fatalf("expected Anthropic error event, got:\n%s", body)
	}
	if strings.Contains(body, "[Error:") {
		t.Fatalf("error should not be emitted as assistant text, got:\n%s", body)
	}
}

// ============================================================
// Anthropic CountTokens
// ============================================================

func TestAnthropic_CountTokens(t *testing.T) {
	mock := &mockProvider{name: "kiro"}
	router := setupRouter(mock, "")

	w := doJSON(router, "POST", "/a/kiro/v1/messages/count_tokens", map[string]any{
		"model":    "claude-sonnet-4-20250514",
		"messages": []map[string]string{{"role": "user", "content": "hello world"}},
	}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
}
