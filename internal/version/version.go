// Package version exposes build-time version and revision information.
package version

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
)

var (
	// Version is the semantic release version. Defaults to the current release base 0.3.1.
	Version = "0.3.1"

	// GitCommit is the commit SHA, usually injected via -ldflags at build time.
	GitCommit = ""

	// BuildDate is the UTC build timestamp (RFC3339), injected via -ldflags.
	BuildDate = ""

	// GoVersion is the Go runtime version.
	GoVersion = runtime.Version()

	// Platform is the target OS and architecture.
	Platform = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
)

// Info represents structured version and build metadata.
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get returns the current version snapshot.
func Get() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		BuildDate: BuildDate,
		GoVersion: GoVersion,
		Platform:  Platform,
	}
}

// String returns a human-readable summary of the version information.
func (i Info) String() string {
	var parts []string
	if i.GitCommit != "" {
		parts = append(parts, fmt.Sprintf("commit: %s", i.GitCommit))
	}
	if i.BuildDate != "" {
		parts = append(parts, fmt.Sprintf("built: %s", i.BuildDate))
	}
	parts = append(parts, fmt.Sprintf("go: %s", i.GoVersion))
	parts = append(parts, i.Platform)

	return fmt.Sprintf("v%s (%s)", strings.TrimPrefix(i.Version, "v"), strings.Join(parts, ", "))
}

// Short returns the normalized version string (e.g. "0.3.0").
func Short() string {
	return strings.TrimPrefix(Version, "v")
}

// Full returns the full human-readable version string.
func Full() string {
	return Get().String()
}

// JSON returns the version info serialized as indented JSON.
func JSON() ([]byte, error) {
	return json.MarshalIndent(Get(), "", "  ")
}
