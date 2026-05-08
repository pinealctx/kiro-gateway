package version

import "runtime/debug"

// Version is overridden via -ldflags in CI builds.
var Version = "dev"

// Get returns the version string. It first checks Go build info
// (set by go install), then falls back to the ldflags-injected value.
func Get() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return Version
}
