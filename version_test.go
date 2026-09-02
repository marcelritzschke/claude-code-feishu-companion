package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersionInfoUsesModuleVersionForGoInstall(t *testing.T) {
	build := &debug.BuildInfo{Main: debug.Module{Version: "v1.0.1"}}

	got := resolveVersionInfo("dev", "none", "unknown", build)

	if got.version != "1.0.1" {
		t.Fatalf("version = %q, want %q", got.version, "1.0.1")
	}
}

func TestResolveVersionInfoPrefersReleaseLinkerValues(t *testing.T) {
	build := &debug.BuildInfo{Main: debug.Module{Version: "v1.0.1"}}

	got := resolveVersionInfo("1.2.3", "abc123", "2026-09-02", build)

	if got.version != "1.2.3" || got.commit != "abc123" || got.date != "2026-09-02" {
		t.Fatalf("version info = %+v, want linker-provided values", got)
	}
}
