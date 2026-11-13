// Command fleet is the CLI and TUI for the agent fleet.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sysx"
	"github.com/sturlabragason/fleet/internal/whatsnew"
)

// daemonCmds are the subcommands that run as fleet's own long-lived
// processes; they drop any agent owner tag they were started with.
var daemonCmds = map[string]bool{"watch": true, "ui": true, "up": true, "wall": true, "web": true}

func main() {
	logx.Init()
	fleet.Version = shortVersion(version, moduleVersion())
	whatsnew.Tag = versionTag(version, moduleVersion())
	if len(os.Args) > 1 && daemonCmds[os.Args[1]] {
		// fleet's own long-lived processes are never an agent's (ADR-122).
		os.Unsetenv(sysx.OwnerEnv)
	}
	if err := run(os.Args[1:]); err != nil {
		var code exitCode
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "fleet:", err)
		os.Exit(1)
	}
}
