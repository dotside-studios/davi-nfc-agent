// Package buildinfo contains application metadata that can be set at build time.
//
// For release builds, use ldflags to set the version:
//
//	go build -ldflags "-X github.com/dotside-studios/davi-nfc-agent/buildinfo.Version=1.0.0"
//
// Or set multiple values:
//
//	go build -ldflags "\
//	  -X github.com/dotside-studios/davi-nfc-agent/buildinfo.Version=1.0.0 \
//	  -X github.com/dotside-studios/davi-nfc-agent/buildinfo.Commit=$(git rev-parse --short HEAD) \
//	  -X github.com/dotside-studios/davi-nfc-agent/buildinfo.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
package buildinfo

import (
	"fmt"
	"runtime"
)

// Application metadata - can be overridden at build time via ldflags
var (
	// Name is the technical application name
	Name = "davi-nfc-agent"

	// ConfigDirName is the name of the config directory within user config paths
	DirName = "davi-nfc-agent"

	// DisplayName is the user-friendly name (used for UI, mDNS, titles)
	DisplayName = "Davi NFC Agent"

	// Description is a short description of the application
	Description = "NFC card reader agent with WebSocket broadcasting"

	// Version is the semantic version (set via ldflags for releases)
	Version = "dev"

	// Commit is the git commit hash (set via ldflags)
	Commit = ""

	// BuildTime is the build timestamp (set via ldflags)
	BuildTime = ""
)

// FullVersion returns the version string with optional commit info.
// Examples:
//   - "dev" (development build)
//   - "1.0.0" (release build)
//   - "1.0.0 (abc1234)" (release build with commit)
func FullVersion() string {
	if Commit != "" {
		return fmt.Sprintf("%s (%s)", Version, Commit)
	}
	return Version
}

// UserAgent returns a user agent string for HTTP requests.
// Example: "davi-nfc-agent/1.0.0"
func UserAgent() string {
	return fmt.Sprintf("%s/%s", Name, Version)
}

// BuildInfo returns a multi-line string with full build information.
func BuildInfo() string {
	info := fmt.Sprintf("%s %s\n", Name, FullVersion())
	info += fmt.Sprintf("  %s\n", Description)
	info += fmt.Sprintf("  Go: %s\n", runtime.Version())
	info += fmt.Sprintf("  OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	if BuildTime != "" {
		info += fmt.Sprintf("\n  Built: %s", BuildTime)
	}
	return info
}

// IsDev returns true if this is a development build.
func IsDev() bool {
	return Version == "dev"
}

// Info is one application's identity: what it calls itself, where it keeps its
// configuration, and which build it is.
//
// The package-level variables above are this repository's own values, stamped
// by its release ldflags, and Default returns them. A program built on top of
// the agent supplies its own Info instead, so it can carry its own name into
// the tray, the console, the pairing pages and the mDNS record — and, more
// importantly, keep its configuration out of this agent's directory.
type Info struct {
	// Name is the technical application name.
	Name string

	// DirName is the configuration directory's name inside the user's config
	// path. Two programs sharing it share their certificates and paired
	// devices, which is rarely what either wants.
	DirName string

	// DisplayName is the user-facing name, used for UI, mDNS and titles.
	DisplayName string

	// Description is a short description, shown in the version banner.
	Description string

	// Version, Commit and BuildTime describe the build.
	Version   string
	Commit    string
	BuildTime string
}

// Default returns this repository's own identity, as stamped at build time.
func Default() Info {
	return Info{
		Name:        Name,
		DirName:     DirName,
		DisplayName: DisplayName,
		Description: Description,
		Version:     Version,
		Commit:      Commit,
		BuildTime:   BuildTime,
	}
}

// OrDefault fills any field left blank from Default, so a caller can override
// only what it cares about. Version is deliberately included: an Info with no
// version reads as a development build, the same as an unstamped binary.
func (i Info) OrDefault() Info {
	d := Default()
	if i.Name == "" {
		i.Name = d.Name
	}
	if i.DirName == "" {
		i.DirName = d.DirName
	}
	if i.DisplayName == "" {
		i.DisplayName = d.DisplayName
	}
	if i.Description == "" {
		i.Description = d.Description
	}
	if i.Version == "" {
		i.Version = d.Version
	}
	return i
}

// FullVersion returns the version string with optional commit info.
func (i Info) FullVersion() string {
	if i.Commit != "" {
		return fmt.Sprintf("%s (%s)", i.Version, i.Commit)
	}
	return i.Version
}

// UserAgent returns a user agent string for HTTP requests.
func (i Info) UserAgent() string {
	return fmt.Sprintf("%s/%s", i.Name, i.Version)
}

// IsDev reports whether this is a development build.
func (i Info) IsDev() bool { return i.Version == "dev" }

// String returns the multi-line banner shown by -version.
func (i Info) String() string {
	info := fmt.Sprintf("%s %s\n", i.Name, i.FullVersion())
	info += fmt.Sprintf("  %s\n", i.Description)
	info += fmt.Sprintf("  Go: %s\n", runtime.Version())
	info += fmt.Sprintf("  OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	if i.BuildTime != "" {
		info += fmt.Sprintf("\n  Built: %s", i.BuildTime)
	}
	return info
}
