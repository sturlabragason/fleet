package prefs

import (
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
)

// Exec is the [exec] section: where `fleet exec` runs heavy commands
// (DESIGN §4, ADR-107). Host "" runs them locally.
type Exec struct {
	// Host is the ssh destination (user@host or a ~/.ssh/config alias).
	Host string
	// Root is the remote directory under the remote home that holds the
	// worktree copies; "" means rexec.DefaultRoot.
	Root string
}

// Exec is [exec], read from config.toml on every call.
func (p *Prefs) Exec() Exec {
	if p == nil {
		return Exec{}
	}
	return p.exec
}

func applyExec(sec map[string]conf.Value) Exec {
	return Exec{Host: strings.TrimSpace(conf.Str(sec, "host")), Root: strings.TrimSpace(conf.Str(sec, "root"))}
}
