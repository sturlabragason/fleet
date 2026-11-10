// Package rexec runs one command for a worktree on another machine: it
// copies the worktree there with rsync, runs the command over ssh in the
// same relative directory and passes its output and exit code back
// (`fleet exec`, DESIGN §4, ADR-107). The agent's loop stays local; only
// the heavy build or test leaves the laptop.
package rexec

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"path/filepath"
	"strings"
)

// DefaultRoot is the remote directory, under the remote home, that holds
// one copy per worktree.
const DefaultRoot = "fleet-exec"

// Plan is what a remote run does: the rsync argv that copies the worktree,
// the ssh argv that runs the command, and the remote directory it runs in.
type Plan struct {
	Sync, Run []string
	Dir       string
}

// RemoteDir is the copy of the worktree at top on the remote side:
// <root>/<base>-<8 hex of the path>, so two worktrees of one repo never
// share a copy.
func RemoteDir(root, top string) string {
	if root == "" {
		root = DefaultRoot
	}
	sum := sha256.Sum256([]byte(top))
	return path.Join(root, filepath.Base(top)+"-"+hex.EncodeToString(sum[:4]))
}

// Make builds the plan for running cmd in dir, which lies inside the
// worktree top. One argument is a shell line run as is; several are quoted
// one by one, so `fleet exec -- go test ./...` and
// `fleet exec -- 'go test ./... | tail'` both do what they say.
func Make(host, root, top, dir string, cmd []string) Plan {
	rd := RemoteDir(root, top)
	rel, err := filepath.Rel(top, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = "."
	}
	wd := path.Join(rd, filepath.ToSlash(rel))
	sync := []string{"rsync", "-az", "--delete", "--mkpath",
		"--filter=:- .gitignore", "--exclude=/.git",
		strings.TrimSuffix(top, "/") + "/", host + ":" + rd + "/"}
	run := []string{"ssh", "-T", host, "--", "cd " + Quote(wd) + " && " + Line(cmd)}
	return Plan{Sync: sync, Run: run, Dir: wd}
}

// Line is cmd as one shell line: a single argument as is, several quoted.
func Line(cmd []string) string {
	if len(cmd) == 1 {
		return cmd[0]
	}
	q := make([]string, len(cmd))
	for i, a := range cmd {
		q[i] = Quote(a)
	}
	return strings.Join(q, " ")
}

// Quote makes s one word for a POSIX shell.
func Quote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,+@%", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
