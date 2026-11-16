package trust

import (
	"path/filepath"
	"strings"
)

// markPi sets dir to true in pi's trust.json, which is what its "Trust"
// answer writes. pi decides by the closest ancestor with an entry, so a dir
// entry of its own wins over a parent pi was told not to trust.
//
// pi keys the file by the dir's real path, so a symlinked dir is resolved
// first (on macOS /var and /tmp are links into /private). Every other entry
// is kept as it was.
func markPi(home string, env Env, dir string) (Result, error) {
	path := filepath.Join(piAgentDir(home, env), "trust.json")
	r := Result{Tool: "pi", File: path}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	changed, err := edit(path, func(old []byte) ([]byte, bool, error) {
		root, err := parseObj(old)
		if err != nil {
			return nil, false, errParse(path, err)
		}
		if root.isTrue(dir) {
			return nil, false, nil
		}
		if err := root.set(dir, true); err != nil {
			return nil, false, err
		}
		b, err := render(root)
		if err != nil {
			return nil, false, err
		}
		return b, true, nil
	})
	r.Changed = changed
	return r, err
}

// piAgentDir is $PI_CODING_AGENT_DIR or ~/.pi/agent.
func piAgentDir(home string, env Env) string {
	if v := strings.TrimSpace(env("PI_CODING_AGENT_DIR")); v != "" {
		return absDir(home, v)
	}
	return filepath.Join(home, ".pi", "agent")
}
