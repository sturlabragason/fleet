package trust

import (
	"bytes"
	"fmt"
	"path/filepath"
)

// markCopilot appends dir to "trustedFolders" in the Copilot CLI's
// config.json. Copilot agents do not stop on a trust dialog today only
// because the folders they are started in are already on that list; a new
// scratch or worktree path is not, so fleet adds it the same way.
//
// The file opens with a "// managed automatically" comment block, which is
// kept as-is above the JSON.
func markCopilot(home string, env Env, dir string) (Result, error) {
	path := filepath.Join(home, ".copilot", "config.json")
	r := Result{Tool: "copilot", File: path}
	changed, err := edit(path, func(old []byte) ([]byte, bool, error) {
		head, body := splitComments(old)
		root, err := parseObj(body)
		if err != nil {
			return nil, false, errParse(path, err)
		}
		folders, ok := root.strings(copilotTrustKey)
		if !ok {
			return nil, false, errParse(path, fmt.Errorf("%s is not a list of folders", copilotTrustKey))
		}
		if contains(folders, dir) {
			return nil, false, nil
		}
		if err := root.set(copilotTrustKey, append(folders, dir)); err != nil {
			return nil, false, err
		}
		b, err := render(root)
		if err != nil {
			return nil, false, err
		}
		return append(bytes.Clone(head), b...), true, nil
	})
	r.Changed = changed
	return r, err
}

// copilotTrustKey lists the folders whose configuration Copilot trusts.
const copilotTrustKey = "trustedFolders"
