package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/sturlabragason/fleet/internal/whatsnew"
)

// version is the release tag, set at link time by the release build
// (-ldflags "-X main.version=v0.4.0") and by make build (git describe).
var version string

func cmdVersion(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: fleet version")
	}
	fmt.Println(versionLine(version, moduleVersion()))
	return nil
}

// versionLine is "fleet <tag> (<rev> · built <time>)" for a tagged build,
// and the build-info form ("fleet <rev> · built <time>") otherwise. A
// `go install …@vX.Y.Z` binary has no ldflags but carries its module version.
func versionLine(linked, module string) string {
	return whatsnew.WithTag(versionTag(linked, module), whatsnew.BuildInfo())
}

// versionTag is the linked release tag, else a `go install` module version.
func versionTag(linked, module string) string {
	if linked == "" && module != "(devel)" {
		return module
	}
	return linked
}

func moduleVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		return bi.Main.Version
	}
	return ""
}

// shortVersion is the release tag, else the build's revision: what the board
// records as the fleet that started an agent (ADR-77).
func shortVersion(linked, module string) string {
	if linked != "" {
		return linked
	}
	if module != "" && module != "(devel)" {
		return module
	}
	rev, _, _ := strings.Cut(strings.TrimPrefix(whatsnew.BuildInfo(), "fleet "), " · ")
	if strings.HasPrefix(rev, "(") {
		return "" // unknown build
	}
	return rev
}

// channel is "release" in a release build (-X main.channel=release in
// .goreleaser.yaml): only those upgrade themselves (ADR-114).
var channel string
