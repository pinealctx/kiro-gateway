package version

import "runtime/debug"

// Version is set via -ldflags in release builds: -X github.com/pinealctx/kiro-gateway/version.Version=v1.0.22
var Version = "v1.0.22"

// Get returns the version string.
// Explicit ldflags value takes priority; falls back to Go build info only when Version is still "dev".
func Get() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return Version
}
