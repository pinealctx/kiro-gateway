package providers

import (
	"context"
	"testing"

	"github.com/pinealctx/kiro-gateway/models"
)

type testProvider struct {
	name    string
	healthy bool
	stopped bool
}

func (p *testProvider) Name() string { return p.name }
func (p *testProvider) ChatCompletion(context.Context, *models.ChatCompletionRequest) (*models.ChatCompletionResponse, error) {
	return nil, nil
}
func (p *testProvider) StreamCompletion(context.Context, *models.ChatCompletionRequest, chan<- StreamChunk) error {
	return nil
}
func (p *testProvider) RefreshToken(context.Context) error { return nil }
func (p *testProvider) IsHealthy(context.Context) bool     { return p.healthy }
func (p *testProvider) Stop()                              { p.stopped = true }

func TestRegistryResolveWithAccountHint(t *testing.T) {
	reg := NewRegistry()
	first := &testProvider{name: "kiro-a", healthy: true}
	second := &testProvider{name: "kiro-b", healthy: true}
	reg.Register(first)
	reg.Register(second)

	got, ok := reg.ResolveWithHint("claude-sonnet-4.5", "kiro-b")
	if !ok || got.Name() != "kiro-b" {
		t.Fatalf("ResolveWithHint() = %v, %v; want kiro-b", got, ok)
	}
}

func TestRegistryDoesNotFilterByModel(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&testProvider{name: "kiro-a", healthy: true})
	reg.Register(&testProvider{name: "kiro-b", healthy: true})

	got, ok := reg.Resolve("kiro-b/claude-sonnet-4.5")
	if !ok || got.Name() != "kiro-a" {
		t.Fatalf("Resolve() = %v, %v; want first healthy account without model filtering", got, ok)
	}
}

func TestRegistryUnregisterStopsProvider(t *testing.T) {
	reg := NewRegistry()
	p := &testProvider{name: "kiro-a", healthy: true}
	reg.Register(p)

	reg.Unregister("kiro-a")

	if !p.stopped {
		t.Fatal("Unregister() did not stop provider")
	}
	if _, ok := reg.Get("kiro-a"); ok {
		t.Fatal("unregistered provider is still resolvable")
	}
}
