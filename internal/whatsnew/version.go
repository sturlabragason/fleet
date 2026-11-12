package whatsnew

import (
	"runtime/debug"
	"strings"
	"time"
)

// Tag is the release tag main was linked with (-X main.version) or its
// module version; main sets it at start. A release or pro build has no VCS
// info (-trimpath, a git archive tree), so without it Version said only
// "fleet (unknown build)".
var Tag string

// Version is "fleet <tag> (<rev> · built <time>)", "fleet <tag>" when the
// build has no VCS info, or BuildInfo when there is no tag.
func Version() string { return WithTag(Tag, BuildInfo()) }

// WithTag labels build (a BuildInfo string) with tag.
func WithTag(tag, build string) string {
	if tag == "" {
		return build
	}
	rest := strings.TrimPrefix(build, "fleet ")
	if rest == "(unknown build)" {
		return "fleet " + tag
	}
	return "fleet " + tag + " (" + rest + ")"
}

// BuildInfo is "fleet <rev>[+dirty] · built YYYY-MM-DD HH:MM" from the build
// info, or "fleet (unknown build)".
func BuildInfo() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return versionOf(nil)
	}
	return versionOf(bi.Settings)
}

func versionOf(settings []debug.BuildSetting) string {
	var rev, at string
	dirty := false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "fleet (unknown build)"
	}
	v := "fleet " + rev[:min(7, len(rev))]
	if dirty {
		v += "+dirty"
	}
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		v += " · built " + t.Local().Format("2006-01-02 15:04")
	}
	return v
}
