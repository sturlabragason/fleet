package trust

import (
	"path/filepath"
	"strings"
)

// markClaude pre-answers Claude Code's two startup modals for dir in its
// config file: the folder-trust dialog, and the one about the external
// imports in the user's own CLAUDE.md, which stops a fresh dir just as hard.
// Claude reads them from the entry of the repo's main checkout, not the cwd,
// so both are marked (claudeProjects). Every other key of the file, and of that project's own entry, is kept.
func markClaude(home string, env Env, dir string) (Result, error) {
	path := claudeConfig(home, env)
	r := Result{Tool: "claude", File: path}
	changed, err := edit(path, func(old []byte) ([]byte, bool, error) {
		root, err := parseObj(old)
		if err != nil {
			return nil, false, errParse(path, err)
		}
		projects, err := root.sub("projects")
		if err != nil {
			return nil, false, errParse(path, err)
		}
		done := true
		for _, key := range claudeProjects(home, dir) {
			entry, err := projects.sub(key)
			if err != nil {
				return nil, false, errParse(path, err)
			}
			complete := true
			for _, k := range claudeKeys {
				if entry.isTrue(k) {
					continue
				}
				complete = false
				if err := entry.set(k, true); err != nil {
					return nil, false, err
				}
			}
			if complete {
				continue
			}
			done = false
			if err := projects.set(key, entry); err != nil {
				return nil, false, err
			}
		}
		if done {
			return nil, false, nil
		}
		if err := root.set("projects", projects); err != nil {
			return nil, false, err
		}
		b, err := render(root)
		return b, true, err
	})
	r.Changed = changed
	return r, err
}

// claudeKeys are the per-project answers Claude Code asks for the first time
// it runs in a dir: "Is this a project you created or one you trust?" and
// "allow external imports" for the CLAUDE.md files the user's own config
// pulls in. Both are modal, so both have to be answered before launch.
var claudeKeys = []string{"hasTrustDialogAccepted", "hasClaudeMdExternalIncludesApproved", "hasClaudeMdExternalIncludesWarningShown"}

// claudeConfig is Claude Code's config file: $CLAUDE_CONFIG_DIR/.claude.json
// when the profile sets one (fleet's claude-personal and claude-work keep two
// accounts apart that way), else ~/.claude.json — which sits beside ~/.claude,
// not inside it.
func claudeConfig(home string, env Env) string {
	if v := strings.TrimSpace(env("CLAUDE_CONFIG_DIR")); v != "" {
		return filepath.Join(absDir(home, v), ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// absDir resolves a config dir that may be relative to home.
func absDir(home, dir string) string {
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(home, dir)
	}
	return filepath.Clean(dir)
}
