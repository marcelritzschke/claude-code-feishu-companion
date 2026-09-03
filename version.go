package main

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// version, commit, and date are set by the release build via
// -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
// Builds outside that pipeline keep these defaults; printVersion supplements
// them with Go's module build information when available.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type versionInfo struct {
	version string
	commit  string
	date    string
}

func printVersion() {
	build, _ := debug.ReadBuildInfo()
	info := resolveVersionInfo(version, commit, date, build)
	fmt.Printf("claude-companion %s (commit %s, built %s)\n", info.version, info.commit, info.date)
}

// resolveVersionInfo preserves release metadata injected by GoReleaser. A
// binary built by `go install module@version` does not receive those linker
// flags, but Go embeds the selected main-module version in its build info.
func resolveVersionInfo(linkedVersion, linkedCommit, linkedDate string, build *debug.BuildInfo) versionInfo {
	info := versionInfo{
		version: linkedVersion,
		commit:  linkedCommit,
		date:    linkedDate,
	}
	if info.version != "dev" || build == nil {
		return info
	}

	moduleVersion := build.Main.Version
	if moduleVersion == "" || moduleVersion == "(devel)" {
		return info
	}
	info.version = strings.TrimPrefix(moduleVersion, "v")
	return info
}
