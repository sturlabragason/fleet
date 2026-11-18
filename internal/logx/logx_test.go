package logx

import (
	"os"
	"path/filepath"
	"testing"
)

// The fleet home ends up 0700 whether it is new or was group/world readable.
func TestPrivateHome(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode // 0: the dir does not exist yet
	}{{"new", 0}, {"open", 0o777}, {"readable", 0o755}, {"private", 0o700}} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "fleet")
			if tc.mode != 0 {
				if err := os.Mkdir(home, tc.mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(home, tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			privateHome(home)
			fi, err := os.Stat(home)
			if err != nil {
				t.Fatal(err)
			}
			if got := fi.Mode().Perm(); got != 0o700 {
				t.Errorf("mode %o, want 700", got)
			}
		})
	}
}
