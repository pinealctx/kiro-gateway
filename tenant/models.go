package tenant

import (
	"strings"
	"time"
)

// APIKey represents a client API key and its allowed Kiro account scope.
type APIKey struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"` // Human-readable label
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// KiroAccounts is the required allow-list of Kiro accounts for this key.
	KiroAccounts       []string `json:"kiro_accounts"`
	KiroDefaultAccount string   `json:"kiro_default_account"`

	// Metadata
	Metadata map[string]string `json:"metadata,omitempty"`
}

// UsageRecord tracks token consumption per request.
type UsageRecord struct {
	ID           int64     `json:"id"`
	KeyID        string    `json:"key_id"`
	Model        string    `json:"model"`
	Provider     string    `json:"provider"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	TotalTokens  int       `json:"total_tokens"`
	Duration     float64   `json:"duration_ms"` // Request duration in ms
	CreatedAt    time.Time `json:"created_at"`
}

// UsageSummary aggregates usage over a time period.
type UsageSummary struct {
	KeyID         string `json:"key_id"`
	KeyName       string `json:"key_name"`
	Model         string `json:"model,omitempty"`
	Provider      string `json:"provider,omitempty"`
	TotalRequests int64  `json:"total_requests"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	TotalTokens   int64  `json:"total_tokens"`
}

// UsageQuery defines parameters for querying usage data.
type UsageQuery struct {
	KeyID    string    // Filter by specific key
	From     time.Time // Start time
	To       time.Time // End time
	Model    string    // Filter by model
	Provider string    // Filter by provider
	GroupBy  string    // Group by: "key" (default), "model", "provider", "key_model"
}

// ProviderRecord represents a persisted Kiro account configuration.
type ProviderRecord struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // always "kiro"
	Region    string    `json:"region"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProviderOption is a functional option for configuring a ProviderRecord.
type ProviderOption func(*ProviderRecord)

func WithProviderType(t string) ProviderOption  { return func(p *ProviderRecord) { p.Type = t } }
func WithProviderEnabled(e bool) ProviderOption { return func(p *ProviderRecord) { p.Enabled = e } }
func WithProviderName(n string) ProviderOption  { return func(p *ProviderRecord) { p.Name = n } }
func WithProviderRegion(r string) ProviderOption {
	return func(p *ProviderRecord) { p.Region = strings.TrimSpace(r) }
}
