package logx

import (
	"bytes"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// After Init a bare slog call lands in fleet.log, not on stderr: the TUI
// draws on that terminal (a threads page sync warning once did).
func TestInitTakesSlogDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_HOME", home)
	var tty bytes.Buffer
	log.SetOutput(&tty)
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old); log.SetOutput(os.Stderr) })
	Init()
	slog.Warn("bare warning", "component", "ui")
	if tty.Len() != 0 {
		t.Errorf("written to the terminal: %q", tty.String())
	}
	data, _ := os.ReadFile(filepath.Join(home, "logs", "fleet.log"))
	if !strings.Contains(string(data), "bare warning") {
		t.Errorf("fleet.log = %q, want the warning", data)
	}
}
