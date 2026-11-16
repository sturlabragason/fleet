package companion

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoDirectExternalTools: outside the core allowlist, fleet runs an
// external binary only through a companion Spec (ADR-126).
func TestNoDirectExternalTools(t *testing.T) {
	// The system shells, git and tmux are fleet's own requirements, not companions.
	// ps is darwin's process table, which Linux reads from /proc (internal/proc).
	core := map[string]bool{"git": true, "tmux": true, "sh": true, "/bin/sh": true, "bash": true, "nice": true, "ionice": true, "taskkill": true, "ps": true}
	re := regexp.MustCompile(`exec\.(?:LookPath|Command)\("([^"]+)"|exec\.CommandContext\([^,()]+,\s*"([^"]+)"`)
	for _, root := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			for n := 1; sc.Scan(); n++ {
				for _, m := range re.FindAllStringSubmatch(sc.Text(), -1) {
					bin := m[1] + m[2]
					if !core[bin] {
						t.Errorf("%s:%d runs %q directly: an external tool is a companion (declare a companion.Spec and call companion.Find; ADR-126, DESIGN §15 Extensions)", path, n, bin)
					}
				}
			}
			return sc.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
