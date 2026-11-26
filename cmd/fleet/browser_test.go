package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sturlabragason/fleet/internal/browser"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// The shim's command opens through the real xdg-open by default and
// refuses, logging the URL, for a never-open agent or while presenting.
func TestBrowserGuardCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh shims")
	}
	cases := []struct {
		name, agent, signal string
		manual, opened      bool
	}{
		{"default allow", "fleet-docs", "", false, true},
		{"never-open agent", "tax-return", "", false, false},
		{"presenting", "fleet-docs", "screen share (gnome-shell-screencast)", false, false},
		{"present on in the tag's home", "fleet-docs", "", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			mark := filepath.Join(t.TempDir(), "opened")
			// The real opener must not see the guard again: xdg-open falls
			// back to $BROWSER, and its PATH finds gio.
			opener := "#!/bin/sh\n[ -n \"$BROWSER\" ] && exit 9\ncase \":$PATH:\" in *:" + browser.Dir(home) + ":*) exit 8;; esac\necho \"$1\" > " + mark + "\n"
			if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte(opener), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := browser.SetPresent(home, c.manual); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FLEET_HOME", t.TempDir()) // the guard takes its home from the owner tag
			t.Setenv("BROWSER", filepath.Join(browser.Dir(home), "xdg-open"))
			t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
			t.Setenv(sysx.OwnerEnv, c.agent+"@"+home)
			t.Setenv("PATH", browser.Dir(home)+string(os.PathListSeparator)+bin)
			old := guardDetect
			guardDetect = func(context.Context) string { return c.signal }
			t.Cleanup(func() { guardDetect = old })

			err := cmdBrowserGuard([]string{"xdg-open", "https://example.test/p"})
			_, statErr := os.Stat(mark)
			if opened := statErr == nil; opened != c.opened {
				t.Fatalf("opened = %v, want %v (err %v)", opened, c.opened, err)
			}
			pending, _ := browser.Pending(home)
			if c.opened {
				if err != nil || len(pending) != 0 {
					t.Errorf("allowed open: err %v, logged %v", err, pending)
				}
				return
			}
			var code exitCode
			if !errors.As(err, &code) || code != 1 {
				t.Errorf("blocked open returned %v, want exit 1", err)
			}
			if len(pending) != 1 || pending[0].URL != "https://example.test/p" || pending[0].Agent != c.agent {
				t.Errorf("blocked log = %+v, want the URL and agent", pending)
			}
		})
	}
}
