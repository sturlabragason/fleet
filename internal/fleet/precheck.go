package fleet

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/tools"
)

// checkDir fails a spawn into a dir that is missing or not a directory, before
// any worktree, board entry or pane exists (fleet#38).
func checkDir(dir string) error {
	st, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("dir %s does not exist", dir)
	case err != nil:
		return fmt.Errorf("dir %s: %w", dir, err)
	case !st.IsDir():
		return fmt.Errorf("dir %s is not a directory", dir)
	}
	return nil
}

// CheckCommand fails when the program the agent's window would exec for this
// profile and driver is not there (fleet#23): the profile's command, or its
// acp_command (and the tool, when acp_env names it) under the acp driver. Without it the pane dies with exit 127
// after a spawn that reported success.
func (f *Fleet) CheckCommand(profile string, p tools.Profile, driver string) error {
	look, exact := f.LookCmd, f.LookCmd
	if look == nil {
		look = lookCmd
		exact = func(c string) error { _, err := commandPath(c); return err }
	}
	type want struct {
		cmd  string
		look func(string) error
	}
	cmds := []want{{p.Command, look}}
	if driver == prefs.DriverACP && len(p.ACPCommand) > 0 {
		// An adapter runs instead of the tool. When acp_env names the tool,
		// acpSetup resolves it without a login shell, so check it that way.
		cmds = []want{{p.ACPCommand[0], look}}
		if p.ACPNeedsCommandPath() {
			cmds = append(cmds, want{p.Command, exact})
		}
	}
	for _, c := range cmds {
		cmd := strings.ReplaceAll(c.cmd, "{fleet}", f.Exe)
		if cmd == "" {
			return fmt.Errorf("profile %s has no command", profile)
		}
		if err := c.look(cmd); err != nil {
			return fmt.Errorf("profile %s: %s not found on PATH", profile, cmd)
		}
	}
	return nil
}

// DefaultDriver is the driver a spawn of profile gets with no --driver
// (driverFor), tmux when that would be refused; fleet doctor checks with it.
func (f *Fleet) DefaultDriver(profile string) string {
	d, err := f.driverFor("", profile)
	if err != nil {
		return prefs.DriverTmux
	}
	return d
}

// lookCmd finds cmd as the window would. The fast path is ~/.local/bin and
// PATH (commandPath); a miss asks a login shell, the window's own shell
// (tools.ShellCommand), whose profile may add dirs this process lacks. A
// login profile that hangs must not hang the spawn.
func lookCmd(cmd string) error {
	if _, err := commandPath(cmd); err == nil || runtime.GOOS == "windows" {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "bash", "-lc", `command -v -- "$1" >/dev/null`, "bash", cmd).Run()
}
