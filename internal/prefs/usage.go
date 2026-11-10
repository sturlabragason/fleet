package prefs

import "github.com/sturlabragason/fleet/internal/conf"

// UsageSection is the raw [usage] section (DESIGN §7b, ADR-97); package
// usage parses it, so its defaults live beside the code that uses them.
func (p *Prefs) UsageSection() map[string]conf.Value {
	if p == nil {
		return nil
	}
	return p.usage
}
