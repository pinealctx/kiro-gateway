package config

// ProviderConfig defines a Kiro account.
type ProviderConfig struct {
	Name    string `mapstructure:"name"`
	Type    string `mapstructure:"type"`    // Kiro-only mode accepts "kiro"
	Enabled bool   `mapstructure:"enabled"` // Whether the account is active
	Region  string `mapstructure:"region"`  // Account login/IDC region; Kiro Q API uses a fixed service region.
}

// GatewayConfig is the top-level YAML configuration.
// Providers are managed at runtime via the Admin API and persisted in SQLite.
type GatewayConfig struct {
	Server   ServerConfig   `mapstructure:"server"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Defaults DefaultsConfig `mapstructure:"defaults"`
	Tenant   TenantConfig   `mapstructure:"tenant"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host        string   `mapstructure:"host"`
	Port        int      `mapstructure:"port"`
	LogLevel    string   `mapstructure:"log_level"`
	CORSOrigins []string `mapstructure:"cors_origins"` // Allowed CORS origins (empty = allow all)
}

// AuthConfig holds auth settings.
type AuthConfig struct {
	AdminKey       string `mapstructure:"admin_key"`        // Separate key for /admin/* endpoints
	AdminLocalOnly bool   `mapstructure:"admin_local_only"` // Restrict /admin/* to loopback clients
}

// DefaultsConfig holds Kiro runtime settings.
type DefaultsConfig struct {
	HealthCheckEnabled       bool `mapstructure:"health_check_enabled"`        // whether to run periodic health checks
	HealthCheckSeconds       int  `mapstructure:"health_check_seconds"`        // health check interval in seconds (default 60)
	FirstTokenTimeoutSeconds int  `mapstructure:"first_token_timeout_seconds"` // stream startup timeout before retrying
	FirstTokenMaxRetries     int  `mapstructure:"first_token_max_retries"`     // retries when no first token arrives
	MaxPayloadBytes          int  `mapstructure:"max_payload_bytes"`           // max serialized Kiro payload size
	AutoTrimPayload          bool `mapstructure:"auto_trim_payload"`           // drop oldest history when payload exceeds MaxPayloadBytes
}

// TenantConfig holds multi-tenant settings.
type TenantConfig struct {
	DBPath string `mapstructure:"db_path"` // SQLite database path for tenant data
}
