package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// BindFlags registers persistent flags on the given cobra.Command.
// Call this once during command initialization (e.g. in init()).
func BindFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()

	// Server
	f.StringP("host", "H", "0.0.0.0", "Listen address (env: HOST)")
	f.IntP("port", "p", 8080, "Listen port (env: PORT)")
	f.StringP("log-level", "l", "info", "Log level: debug|info|warn|error (env: LOG_LEVEL)")
	f.String("log-format", "console", "Log format: console|json (env: LOG_FORMAT)")

	// Auth
	f.String("admin-key", "", "Admin key for /admin/* endpoints (env: ADMIN_KEY)")
	f.Bool("admin-local-only", true, "Restrict /admin/* endpoints to localhost clients (env: ADMIN_LOCAL_ONLY)")

	// Health check
	f.Bool("no-health-check", false, "Disable provider health checks (env: NO_HEALTH_CHECK)")
	f.Int("health-check-interval", 60, "Health check interval in seconds (env: HEALTH_CHECK_INTERVAL)")

	// Tenant
	f.String("db-path", DefaultDBPath(), "SQLite database path (env: DB_PATH)")

	// UI
	f.BoolP("open", "o", false, "Open UI in browser on startup (env: OPEN_UI)")

	// Config file
	f.StringP("config", "c", "", "Path to config file (YAML/TOML/JSON)")
}

// DefaultDBPath returns the default SQLite path in the user-scoped global directory.
// Fallback to a relative path when home directory cannot be resolved.
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "kiro-gateway.db"
	}
	return filepath.Join(home, ".kiro-gateway", "kiro-gateway.db")
}

// LoadGatewayConfig loads the full gateway configuration.
// Config path priority is --config > KIRO_GATEWAY_CONFIG.
// With a config file, explicitly set CLI flags override file values.
// Without a config file, priority is CLI flags > environment variables > defaults.
func LoadGatewayConfig(cmd *cobra.Command) (*GatewayConfig, error) {
	configFile := resolveStr(cmd, "config", "KIRO_GATEWAY_CONFIG")

	if configFile != "" {
		return loadFromFile(configFile, cmd)
	}
	return synthesizeFromFlags(cmd), nil
}

func loadFromFile(path string, cmd *cobra.Command) (*GatewayConfig, error) {
	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var gw GatewayConfig
	if err := v.Unmarshal(&gw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// CLI flags override file values when explicitly set
	if cmd.Flags().Changed("host") {
		gw.Server.Host, _ = cmd.Flags().GetString("host")
	}
	if cmd.Flags().Changed("port") {
		gw.Server.Port, _ = cmd.Flags().GetInt("port")
	}
	if cmd.Flags().Changed("log-level") {
		gw.Server.LogLevel, _ = cmd.Flags().GetString("log-level")
		gw.Server.LogLevel = strings.ToLower(gw.Server.LogLevel)
	}
	if cmd.Flags().Changed("log-format") {
		gw.Server.LogFormat, _ = cmd.Flags().GetString("log-format")
		gw.Server.LogFormat = strings.ToLower(gw.Server.LogFormat)
	}
	if cmd.Flags().Changed("admin-key") {
		gw.Auth.AdminKey, _ = cmd.Flags().GetString("admin-key")
	}
	if cmd.Flags().Changed("admin-local-only") {
		gw.Auth.AdminLocalOnly, _ = cmd.Flags().GetBool("admin-local-only")
	} else if !v.IsSet("auth.admin_local_only") {
		gw.Auth.AdminLocalOnly = true
	}
	if cmd.Flags().Changed("no-health-check") {
		noHealthCheck, _ := cmd.Flags().GetBool("no-health-check")
		gw.Defaults.HealthCheckEnabled = !noHealthCheck
	}
	if cmd.Flags().Changed("health-check-interval") {
		gw.Defaults.HealthCheckSeconds, _ = cmd.Flags().GetInt("health-check-interval")
	}
	if cmd.Flags().Changed("db-path") {
		gw.Tenant.DBPath, _ = cmd.Flags().GetString("db-path")
	}

	// Apply defaults for missing fields
	if gw.Server.Host == "" {
		gw.Server.Host = "0.0.0.0"
	}
	if gw.Server.Port == 0 {
		gw.Server.Port = 8080
	}
	if gw.Server.LogLevel == "" {
		gw.Server.LogLevel = "info"
	} else {
		gw.Server.LogLevel = strings.ToLower(gw.Server.LogLevel)
	}
	if gw.Server.LogFormat == "" {
		gw.Server.LogFormat = "console"
	} else {
		gw.Server.LogFormat = strings.ToLower(gw.Server.LogFormat)
	}
	if gw.Defaults.HealthCheckSeconds == 0 {
		gw.Defaults.HealthCheckSeconds = 60
	}
	if gw.Tenant.DBPath == "" {
		gw.Tenant.DBPath = DefaultDBPath()
	}
	applyNotificationDefaults(&gw)

	return &gw, nil
}

// synthesizeFromFlags builds a GatewayConfig from CLI flags only (no config file).
func synthesizeFromFlags(cmd *cobra.Command) *GatewayConfig {
	// Server
	host := resolveStr(cmd, "host", "HOST")
	port := resolveInt(cmd, "port", "PORT")
	logLevel := strings.ToLower(resolveStr(cmd, "log-level", "LOG_LEVEL"))
	logFormat := strings.ToLower(resolveStr(cmd, "log-format", "LOG_FORMAT"))

	// Auth
	adminKey := resolveStr(cmd, "admin-key", "ADMIN_KEY")
	adminLocalOnly := resolveBool(cmd, "admin-local-only", "ADMIN_LOCAL_ONLY")

	// Health check
	noHealthCheck := resolveBool(cmd, "no-health-check", "NO_HEALTH_CHECK")
	healthCheckInterval := resolveInt(cmd, "health-check-interval", "HEALTH_CHECK_INTERVAL")

	// Tenant
	dbPath := resolveStr(cmd, "db-path", "DB_PATH")

	// Apply defaults
	if host == "" {
		host = "0.0.0.0"
	}
	if port == 0 {
		port = 8080
	}
	if logLevel == "" {
		logLevel = "info"
	}
	if logFormat == "" {
		logFormat = "console"
	}
	if healthCheckInterval == 0 {
		healthCheckInterval = 60
	}
	if dbPath == "" {
		dbPath = DefaultDBPath()
	}

	gw := &GatewayConfig{
		Server: ServerConfig{
			Host:      host,
			Port:      port,
			LogLevel:  logLevel,
			LogFormat: logFormat,
		},
		Auth: AuthConfig{
			AdminKey:       adminKey,
			AdminLocalOnly: adminLocalOnly,
		},
		Defaults: DefaultsConfig{
			HealthCheckEnabled: !noHealthCheck,
			HealthCheckSeconds: healthCheckInterval,
		},
		Tenant: TenantConfig{
			DBPath: dbPath,
		},
	}
	applyNotificationEnv(gw)
	applyNotificationDefaults(gw)
	return gw
}

func applyNotificationEnv(gw *GatewayConfig) {
	if v := os.Getenv("TEAMS_WEBHOOK_URL"); v != "" {
		gw.Notifications.Teams.WebhookURL = v
		gw.Notifications.Teams.Enabled = true
	}
}

func applyNotificationDefaults(gw *GatewayConfig) {
	teams := &gw.Notifications.Teams
	teams.WebhookURL = expandEnvValue(teams.WebhookURL)
	if teams.WebhookURL == "" {
		teams.WebhookURL = os.Getenv("TEAMS_WEBHOOK_URL")
	}
	if teams.WebhookURL != "" {
		teams.Enabled = true
	}
	if !teams.Enabled {
		return
	}
	if len(teams.AccountThresholds) == 0 {
		teams.AccountThresholds = []float64{90, 100}
	}
	if len(teams.TotalThresholds) == 0 {
		teams.TotalThresholds = []float64{90, 100}
	}
	if len(teams.DailyTimes) == 0 {
		teams.DailyTimes = []string{"09:00"}
	}
	if teams.Timezone == "" {
		teams.Timezone = "Local"
	}
	if teams.CheckIntervalSeconds <= 0 {
		teams.CheckIntervalSeconds = 10 * 60
	}
	sort.Float64s(teams.AccountThresholds)
	sort.Float64s(teams.TotalThresholds)
}

func expandEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		return os.Getenv(strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}"))
	}
	return os.ExpandEnv(value)
}

// resolveStr: flag (if explicitly set) > env > flag default.
func resolveStr(cmd *cobra.Command, flag, envKey string) string {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetString(flag)
		return v
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	v, _ := cmd.Flags().GetString(flag)
	return v
}

func resolveInt(cmd *cobra.Command, flag, envKey string) int {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetInt(flag)
		return v
	}
	if v := os.Getenv(envKey); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	v, _ := cmd.Flags().GetInt(flag)
	return v
}

func resolveBool(cmd *cobra.Command, flag, envKey string) bool {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetBool(flag)
		return v
	}
	if v := os.Getenv(envKey); v != "" {
		return strings.ToLower(v) == "true" || v == "1"
	}
	v, _ := cmd.Flags().GetBool(flag)
	return v
}
