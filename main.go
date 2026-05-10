package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/pinealctx/kiro-gateway/api/routes"
	"github.com/pinealctx/kiro-gateway/config"
	"github.com/pinealctx/kiro-gateway/core/providers"
	"github.com/pinealctx/kiro-gateway/providers/kiro"
	"github.com/pinealctx/kiro-gateway/tenant"
	"github.com/pinealctx/kiro-gateway/version"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:     "kiro-gateway",
	Short:   "Kiro Gateway",
	Long:    "Kiro Gateway exposes OpenAI / Anthropic-compatible endpoints with multiple accounts.",
	Version: version.Get(),
	RunE:    runServe,
}

func init() {
	config.BindFlags(rootCmd)
}

func runServe(cmd *cobra.Command, _ []string) error {
	bootstrapLogger, _ := newLogger("info", "console")
	gwCfg, err := config.LoadGatewayConfig(cmd)
	if err != nil {
		bootstrapLogger.Fatal("Failed to load config", zap.Error(err))
	}

	logger, err := newLogger(gwCfg.Server.LogLevel, gwCfg.Server.LogFormat)
	if err != nil {
		bootstrapLogger.Fatal("Failed to init logger", zap.Error(err))
	}
	defer func() { _ = logger.Sync() }()

	// Generate admin key if not specified
	if gwCfg.Auth.AdminKey == "" {
		gwCfg.Auth.AdminKey = generateRandomKey()
		logger.Info("Generated admin key (save this for future use)",
			zap.String("admin_key", gwCfg.Auth.AdminKey),
		)
	}

	logger.Info("Starting Kiro Gateway",
		zap.String("version", version.Get()),
		zap.String("host", gwCfg.Server.Host),
		zap.Int("port", gwCfg.Server.Port),
		zap.String("log_format", gwCfg.Server.LogFormat),
	)
	kiro.ConfigureRuntime(kiro.RuntimeConfig{
		MaxPayloadBytes: gwCfg.Defaults.MaxPayloadBytes,
		AutoTrimPayload: gwCfg.Defaults.AutoTrimPayload,
	})

	// Initialize Kiro account registry.
	registry := providers.NewRegistry()

	// SQLite store — always created for provider & key management
	dbPath := gwCfg.Tenant.DBPath
	if dbPath == "" {
		dbPath = config.DefaultDBPath()
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		logger.Fatal("Failed to create db directory", zap.Error(err))
	}
	store, err := tenant.NewStore(dbPath)
	if err != nil {
		logger.Fatal("Failed to init store", zap.Error(err))
	}
	defer func() { _ = store.Close() }()
	logger.Info("Store initialized", zap.String("db", dbPath))

	// Load dynamically-managed providers from DB
	for _, rec := range store.ListProviderRecords() {
		if !rec.Enabled {
			logger.Info("Skipping disabled provider", zap.String("name", rec.Name))
			continue
		}
		if rec.Type != "kiro" {
			logger.Warn("Skipping unsupported account type",
				zap.String("name", rec.Name),
				zap.String("type", rec.Type))
			continue
		}
		pc := config.ProviderConfig{
			Name:    rec.Name,
			Type:    rec.Type,
			Region:  rec.Region,
			Enabled: rec.Enabled,
		}
		p, err := createProvider(pc, logger)
		if err != nil {
			logger.Error("Failed to create provider", zap.String("name", rec.Name), zap.Error(err))
			continue
		}
		registry.Register(p)
		logger.Info("Registered provider",
			zap.String("name", rec.Name),
			zap.String("type", rec.Type),
		)
	}

	// Start background health checks
	healthCheckEnabled := gwCfg.Defaults.HealthCheckEnabled
	if healthCheckEnabled {
		healthCheckSeconds := gwCfg.Defaults.HealthCheckSeconds
		if healthCheckSeconds <= 0 {
			healthCheckSeconds = 60 // default 60 seconds
		}
		registry.StartHealthCheck(time.Duration(healthCheckSeconds) * time.Second)
		logger.Info("Health check started", zap.Int("interval_seconds", healthCheckSeconds))
	} else {
		logger.Info("Health check disabled by config")
	}

	// Inject store into Kiro providers and restore persisted tokens
	for _, p := range registry.All() {
		if kp, ok := p.(*kiro.Provider); ok {
			kp.SetStore(store)
			if kp.RestoreToken() {
				logger.Info("Kiro token restored from persistent storage")
			}
		}
	}

	routerCfg := routes.RouterConfig{
		Registry:        registry,
		Logger:          logger,
		AdminKey:        gwCfg.Auth.AdminKey,
		AdminLocalOnly:  gwCfg.Auth.AdminLocalOnly,
		Store:           store,
		CORSOrigins:     gwCfg.Server.CORSOrigins,
		ProviderFactory: createProvider,
	}

	// Setup Gin router
	r := routes.SetupRouter(routerCfg)

	// Graceful shutdown
	addr := fmt.Sprintf("%s:%d", gwCfg.Server.Host, gwCfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Build UI URL (use localhost for browser access)
	uiURL := fmt.Sprintf("http://127.0.0.1:%d/ui", gwCfg.Server.Port)

	// Start server in goroutine
	go func() {
		logger.Info("Server listening",
			zap.String("addr", addr),
			zap.String("ui", uiURL),
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server error", zap.Error(err))
		}
	}()

	// Open browser if --open flag is set
	openUI, _ := cmd.Flags().GetBool("open")
	if openUI {
		go func() {
			// Wait a moment for server to start
			time.Sleep(500 * time.Millisecond)
			if err := openBrowser(uiURL); err != nil {
				logger.Warn("Failed to open browser", zap.Error(err))
			}
		}()
	}

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("Shutting down server...", zap.String("signal", sig.String()))

	// Give active connections 30 seconds to finish
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", zap.Error(err))
		return err
	}

	// Stop background goroutines
	for _, p := range registry.All() {
		if s, ok := p.(providers.Stoppable); ok {
			s.Stop()
		}
	}

	logger.Info("Server exited gracefully")
	return nil
}

// createProvider instantiates an AIProvider from config.
func createProvider(pc config.ProviderConfig, logger *zap.Logger) (providers.AIProvider, error) {
	if pc.Type != "" && pc.Type != "kiro" {
		return nil, fmt.Errorf("unsupported account type: %q", pc.Type)
	}
	name := pc.Name
	if name == "" {
		name = "kiro"
	}
	return kiro.NewProvider(name, logger, pc.Region), nil
}

func newLogger(level, format string) (*zap.Logger, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "console"
	}
	if format != "console" && format != "json" {
		return nil, fmt.Errorf("invalid log format %q: must be console or json", format)
	}

	cfg := zap.NewProductionConfig()
	cfg.Encoding = format
	if format == "console" {
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		cfg.EncoderConfig.EncodeDuration = zapcore.StringDurationEncoder
	}
	parsedLevel := zapcore.InfoLevel
	if strings.TrimSpace(level) != "" {
		if err := parsedLevel.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(level)))); err != nil {
			return nil, fmt.Errorf("invalid log level %q: %w", level, err)
		}
	}
	cfg.Level = zap.NewAtomicLevelAt(parsedLevel)
	return cfg.Build()
}

// generateRandomKey creates a secure random key.
func generateRandomKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// openBrowser opens the specified URL in the default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default: // linux, freebsd, etc.
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
