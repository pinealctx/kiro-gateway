package providers

import (
	"context"
	"sync"
	"time"

	"github.com/pinealctx/kiro-gateway/models"
)

// StreamChunk represents one piece of a streaming response.
type StreamChunk struct {
	Content          string
	ReasoningContent string
	ToolCalls        []models.ToolCall
	FinishReason     string
	Error            error
}

// AIProvider is implemented by a Kiro account.
type AIProvider interface {
	Name() string
	ChatCompletion(ctx context.Context, req *models.ChatCompletionRequest) (*models.ChatCompletionResponse, error)
	StreamCompletion(ctx context.Context, req *models.ChatCompletionRequest, stream chan<- StreamChunk) error
	RefreshToken(ctx context.Context) error
	IsHealthy(ctx context.Context) bool
}

type Stoppable interface {
	Stop()
}

type TokenInfoProvider interface {
	GetTokenInfo() map[string]any
}

type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

type ProviderEntry struct {
	Provider AIProvider
	healthy  bool
}

// Registry is a Kiro account registry.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*ProviderEntry
	order   []string
}

func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[string]*ProviderEntry),
	}
}

func (r *Registry) Register(p AIProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	healthy := p.IsHealthy(ctx)
	cancel()

	if _, exists := r.entries[p.Name()]; !exists {
		r.order = append(r.order, p.Name())
	}
	r.entries[p.Name()] = &ProviderEntry{
		Provider: p,
		healthy:  healthy,
	}
}

func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	old := r.entries[name]
	delete(r.entries, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	r.mu.Unlock()

	if old != nil {
		if s, ok := old.Provider.(Stoppable); ok {
			s.Stop()
		}
	}
}

func (r *Registry) Get(name string) (AIProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[name]; ok {
		return e.Provider, true
	}
	return nil, false
}

func (r *Registry) Resolve(model string) (AIProvider, bool) {
	return r.ResolveWithHint(model, "")
}

func (r *Registry) ResolveWithHint(model, accountHint string) (AIProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if accountHint != "" {
		if e, ok := r.entries[accountHint]; ok && e.healthy {
			return e.Provider, true
		}
		return nil, false
	}

	for _, name := range r.order {
		e := r.entries[name]
		if e != nil && e.healthy {
			return e.Provider, true
		}
	}
	return nil, false
}

func (r *Registry) CheckHealthFor(name string) {
	r.mu.RLock()
	entry, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	healthy := entry.Provider.IsHealthy(ctx)
	cancel()
	r.SetHealthy(name, healthy)
}

func (r *Registry) SetHealthy(name string, healthy bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[name]; ok {
		e.healthy = healthy
	}
}

func (r *Registry) IsHealthy(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[name]; ok {
		return e.healthy
	}
	return false
}

func (r *Registry) StartHealthCheck(interval time.Duration) {
	checkAll := func() {
		r.mu.RLock()
		entries := make(map[string]*ProviderEntry, len(r.entries))
		for k, v := range r.entries {
			entries[k] = v
		}
		r.mu.RUnlock()

		for name, entry := range entries {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			healthy := entry.Provider.IsHealthy(ctx)
			cancel()
			r.SetHealthy(name, healthy)
		}
	}

	go func() {
		initialDelay := 5 * time.Second
		if interval > 0 && interval < initialDelay {
			initialDelay = interval
		}
		time.Sleep(initialDelay)
		checkAll()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			checkAll()
		}
	}()
}

func (r *Registry) All() map[string]AIProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]AIProvider, len(r.entries))
	for name, e := range r.entries {
		result[name] = e.Provider
	}
	return result
}

func (r *Registry) Entries() map[string]*ProviderEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]*ProviderEntry, len(r.entries))
	for k, v := range r.entries {
		result[k] = v
	}
	return result
}
