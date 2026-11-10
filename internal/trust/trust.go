// Package trust pre-marks a directory as trusted in an agent tool's own
// config file, the way the tool itself would write it, so its "do you trust
// this folder?" dialog never stops a fleet agent (DESIGN.md §4, ADR-47).
//
// Each tool keeps the answer somewhere different:
//
//	claude   <CLAUDE_CONFIG_DIR>/.claude.json (default ~/.claude.json),
//	         projects["<dir>"] and projects["<repo main checkout>"]: trust + imports
//	copilot  ~/.copilot/config.json, "trustedFolders": ["<dir>", …]
//	vibe     <VIBE_HOME>/trusted_folders.toml (default ~/.vibe), trusted = […]
//	agy      no trust store fleet can find; its dialog is answered on screen
//
// Every write merges into whatever is in the file right now and replaces it
// atomically, because the tool itself may be writing the same file.
package trust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupported means the tool keeps no trust store fleet knows how to
// write. The caller carries on: the dialog, if any, is answered on screen.
var ErrUnsupported = errors.New("no known trust store")

// Result describes one Mark.
type Result struct {
	Tool    string // tool whose store was written
	File    string // the file
	Changed bool   // false: the dir was already trusted, nothing written
}

// Env looks up one variable of the agent's environment.
type Env func(string) string

type marker func(home string, env Env, dir string) (Result, error)

var markers = map[string]marker{
	"claude":  markClaude,
	"copilot": markCopilot,
	"vibe":    markVibe,
}

// Tools lists the tool commands with a trust store, for docs and messages.
func Tools() []string { return []string{"claude", "copilot", "vibe"} }

// Mark marks dir trusted for the tool that command runs. env is the agent's
// environment (a profile's own CLAUDE_CONFIG_DIR or VIBE_HOME wins over the
// process environment); nil falls back to os.Getenv. dir must be absolute.
//
// ErrUnsupported: no store for that command, nothing was written.
func Mark(command string, env map[string]string, dir string) (Result, error) {
	m, ok := markers[toolOf(command)]
	if !ok {
		return Result{}, fmt.Errorf("%s: %w", toolOf(command), ErrUnsupported)
	}
	if !filepath.IsAbs(dir) {
		return Result{}, fmt.Errorf("dir %q is not absolute", dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	return m(home, lookup(env), filepath.Clean(dir))
}

// Supported reports whether Mark knows the tool that command runs.
func Supported(command string) bool {
	_, ok := markers[toolOf(command)]
	return ok
}

// toolOf maps a profile command to a tool name: the base name without a
// path or a .exe suffix, so "~/.local/bin/claude" is still claude.
func toolOf(command string) string {
	b := filepath.Base(strings.TrimSpace(command))
	return strings.TrimSuffix(b, ".exe")
}

// lookup prefers the agent's own environment, expanding $VAR in its values
// the way tools.Profile.EnvList does, then falls back to the process one.
func lookup(env map[string]string) Env {
	return func(k string) string {
		if v, ok := env[k]; ok {
			return os.ExpandEnv(v)
		}
		return os.Getenv(k)
	}
}
