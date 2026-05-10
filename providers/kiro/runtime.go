package kiro

type RuntimeConfig struct {
	MaxPayloadBytes int
	AutoTrimPayload bool
}

var runtimeConfig = RuntimeConfig{
	MaxPayloadBytes: 0,
	AutoTrimPayload: false,
}

func ConfigureRuntime(cfg RuntimeConfig) {
	if cfg.MaxPayloadBytes >= 0 {
		runtimeConfig.MaxPayloadBytes = cfg.MaxPayloadBytes
	}
	runtimeConfig.AutoTrimPayload = cfg.AutoTrimPayload
}
