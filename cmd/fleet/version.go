package main

import (
	"fmt"
	"runtime/debug"

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
	build := whatsnew.Version()
	tag := linked
	if tag == "" && module != "" && module != "(devel)" {
		tag = module
	}
	if tag == "" {
		return build
	}
	rest := build[len("fleet "):]
	if rest == "(unknown build)" {
		return "fleet " + tag
	}
	return "fleet " + tag + " (" + rest + ")"
}

func moduleVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		return bi.Main.Version
	}
	return ""
}
