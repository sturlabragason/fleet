package prefs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
)

// Squad is the [squad] section: the caps on a leader's own agents
// (DESIGN §6.11, ADR-53).
type Squad struct {
	// MaxMembers is how many live agents one leader may have at once.
	MaxMembers int
	// MaxDepth is how deep the hierarchy may go under the admiral: 1 means
	// a leader may spawn members but a member may not itself lead.
	MaxDepth int
}

// Defaults for [squad] (DESIGN §6.11).
const (
	DefaultMaxMembers = 4
	DefaultMaxDepth   = 1
)

// DefaultSquad is the caps without a config file: 4 members, depth 1.
func DefaultSquad() Squad { return Squad{MaxMembers: DefaultMaxMembers, MaxDepth: DefaultMaxDepth} }

// Squad is [squad]: the squad caps. Like [memory] it is read live, so a
// change takes effect without restarting fleet.
func (p *Prefs) Squad() Squad {
	if p == nil {
		return DefaultSquad()
	}
	return p.squad
}

func applySquad(s Squad, sec map[string]conf.Value, path string) (Squad, error) {
	for key, dst := range map[string]*int{"max_members": &s.MaxMembers, "max_depth": &s.MaxDepth} {
		v := strings.TrimSpace(conf.Str(sec, key))
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return s, fmt.Errorf("%s: %s %q: want a whole number >= 0", path, key, v)
		}
		*dst = n
	}
	return s, nil
}
