package trust

import (
	"os"
	"path/filepath"
	"strings"
)

// claudeProjects lists the "projects" keys Claude Code may read for an agent
// started in dir: dir itself, and the canonical root of the git repo around
// it. Claude keys its per-project config by that root, not by the cwd, and
// for a linked worktree the root is the main checkout's. So an agent started
// in a repo subdir or a worktree reads an entry fleet never wrote unless
// both are marked. A root at home or / (a dotfiles repo) is never marked:
// that would trust every dir under it (ADR-47).
func claudeProjects(home, dir string) []string {
	keys := []string{dir}
	if r := canonicalRoot(dir); r != "" && r != dir && r != filepath.Clean(home) && r != string(filepath.Separator) {
		keys = append(keys, r)
	}
	return keys
}

// canonicalRoot finds the git repo dir is in and returns its main checkout's
// root, read from the .git entry the way git lays it out (no git process):
// a .git dir is its own root; a .git file points at a worktree's gitdir,
// whose commondir is the main repo's .git. "" when dir is in no repo.
func canonicalRoot(dir string) string {
	for d := dir; ; {
		fi, err := os.Stat(filepath.Join(d, ".git"))
		if err == nil {
			if fi.IsDir() {
				return d
			}
			return mainCheckout(d)
		}
		up := filepath.Dir(d)
		if up == d {
			return ""
		}
		d = up
	}
}

// mainCheckout resolves the .git file of a linked worktree at root to the
// main checkout. A submodule or a bare common dir has none, and root stands.
func mainCheckout(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return root
	}
	gitdir = abs(root, strings.TrimSpace(gitdir))
	c, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return root
	}
	common := abs(gitdir, strings.TrimSpace(string(c)))
	if filepath.Base(common) != ".git" {
		return root
	}
	return filepath.Dir(common)
}

func abs(base, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}
