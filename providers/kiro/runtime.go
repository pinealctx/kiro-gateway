package kiro

import "time"

type RuntimeConfig struct {
	FirstTokenTimeout time.Duration
	FirstTokenRetries int
	MaxPayloadBytes   int
	AutoTrimPayload   bool
}

var runtimeConfig = RuntimeConfig{
	FirstTokenTimeout: 15 * time.Second,
	FirstTokenRetries: 3,
	MaxPayloadBytes:   600000,
	AutoTrimPayload:   false,
}

func ConfigureRuntime(cfg RuntimeConfig) {
	if cfg.FirstTokenTimeout > 0 {
		runtimeConfig.FirstTokenTimeout = cfg.FirstTokenTimeout
	}
	if cfg.FirstTokenRetries >= 0 {
		runtimeConfig.FirstTokenRetries = cfg.FirstTokenRetries
	}
	if cfg.MaxPayloadBytes >= 0 {
		runtimeConfig.MaxPayloadBytes = cfg.MaxPayloadBytes
	}
	runtimeConfig.AutoTrimPayload = cfg.AutoTrimPayload
}
