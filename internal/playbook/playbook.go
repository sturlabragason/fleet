// Package playbook holds the fleet worker playbook: the standard process rules
// prepended to an agent's first prompt at spawn (DESIGN §4, ADR-22). It comes
// in three effort levels (ADR-57), and the built-in text can be extended or
// replaced per user (~/.fleet/worker.md) and per repo (.fleet/worker.md).
package playbook

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Level is how much process a spawn gets (ADR-57). The playbook is paid for
// on every spawn and over-steers a small job, so a timer or a lookup gets a
// few lines and only professional code work gets the whole checklist.
type Level string

const (
	// Light: stay in your dir, no secrets, report. For errands and quick
	// read-only questions.
	Light Level = "light"
	// Standard: plan, build and test what you changed, commit on your
	// branch. No ADR, no subagent review, no merge unless asked.
	Standard Level = "standard"
	// Pro: the full worker process, including the pre-merge checklist.
	Pro Level = "pro"
)

// Levels are the valid effort levels, lightest first.
var Levels = []Level{Light, Standard, Pro}

// ParseLevel reads an effort level from a CLI flag or an MCP argument. An
// empty string means "not given" and is not a level: callers use DefaultLevel.
func ParseLevel(s string) (Level, error) {
	for _, l := range Levels {
		if strings.EqualFold(strings.TrimSpace(s), string(l)) {
			return l, nil
		}
	}
	return "", fmt.Errorf("unknown effort %q (have %s, %s, %s)", s, Light, Standard, Pro)
}

// DefaultLevel is the level a spawn that did not ask for one gets: pro when
// the agent got its own git worktree, which is the professional code task the
// full playbook was written for, and light otherwise (worktree=false, or a dir
// outside any repo).
func DefaultLevel(worktree bool) Level {
	if worktree {
		return Pro
	}
	return Light
}

// ReplaceMarker as the first line of a layer replaces everything before it
// instead of appending. A layer with nothing after the marker turns the
// playbook off.
const ReplaceMarker = "<!-- fleet:replace -->"

// BuiltIn is the source name of the embedded default; Resolve appends the
// level to it.
const BuiltIn = "built-in"

//go:embed worker.md
var worker string

//go:embed light.md
var light string

//go:embed standard.md
var standard string

//go:embed leader.md
var leader string

// Leader is the orientation a squad leader gets in its first prompt: which
// squad tools it has, that its agents report to it, and that the fleet CLI
// is not how it spawns them (ADR-58). The MCP server also sends it as
// instructions, but only a prompt is guaranteed to be read on turn one.
func Leader() string { return leader }

// Text returns the built-in playbook for a level. An unknown level reads as
// pro: more process is the safe way to be wrong.
func Text(l Level) string {
	switch l {
	case Light:
		return light
	case Standard:
		return standard
	}
	return worker
}

// Default returns the built-in pro playbook.
func Default() string { return worker }

// Resolve returns the effective playbook for an agent working in dir at
// effort level l: the built-in text for that level, then <home>/worker.md,
// then the nearest .fleet/worker.md at or above dir within the repo. The
// user's and the repo's layers apply at every level. sources lists what was
// used, in order.
func Resolve(l Level, home, dir string) (text string, sources []string) {
	text, sources = Text(l), []string{BuiltIn + " (" + string(l) + ")"}
	files := []string{filepath.Join(home, "worker.md")}
	if p := repoFile(dir); p != "" {
		files = append(files, p)
	}
	for _, p := range files {
		layer, ok := read(p)
		if !ok {
			continue
		}
		text = apply(text, layer)
		sources = append(sources, p)
	}
	return text, sources
}

// apply layers one file's text onto the playbook so far.
func apply(text, layer string) string {
	first, rest, _ := strings.Cut(layer, "\n")
	if strings.TrimSpace(first) == ReplaceMarker {
		return strings.TrimSpace(rest)
	}
	if strings.TrimSpace(text) == "" {
		return layer
	}
	return strings.TrimRight(text, "\n") + "\n\n" + layer
}

// read returns a layer file's text; ok is false when it is missing, empty or
// unreadable.
func read(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logx.For("playbook").Warn("unreadable; skipped", "path", path, "err", err)
		}
		return "", false
	}
	s := strings.TrimSpace(string(b))
	return s, s != ""
}

// repoFile walks up from dir to the first directory holding a .git entry (a
// worktree has a .git file), the user's home directory or the filesystem
// root, and returns the first .fleet/worker.md it finds on the way, or "".
// The walk stops below the home directory so that ~/.fleet/worker.md stays
// the user layer and is not also applied as a repo layer (ADR-58).
func repoFile(dir string) string {
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	userHome, _ := os.UserHomeDir()
	for {
		if userHome != "" && dir == userHome {
			return ""
		}
		p := filepath.Join(dir, ".fleet", "worker.md")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Compose prepends the playbook to a first prompt. An empty prompt or an
// empty playbook leaves the prompt unchanged.
func Compose(playbook, prompt string) string {
	if prompt == "" || playbook == "" {
		return prompt
	}
	return strings.TrimSpace(playbook) + "\n\n## Your task\n\n" + prompt
}
