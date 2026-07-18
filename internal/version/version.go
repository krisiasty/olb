// Package version holds build-time version information for olb.
package version

// These values are overridden at build time via -ldflags, e.g.:
//
//	go build -ldflags "-X github.com/krisiasty/olb/internal/version.Version=v1.0.0"
var (
	// Version is the semantic version of the build.
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "none"
	// Date is the build date (RFC3339).
	Date = "unknown"
)

// String returns a human-readable version line.
func String() string {
	return "olb " + Version + " (commit " + Commit + ", built " + Date + ")"
}
