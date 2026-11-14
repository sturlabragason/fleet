package prefs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/conf"
)

// Integration is one [integrations.<name>] section (ADR-126): path is
// "auto" (PATH, the default), "off", or a binary, optionally with
// arguments; any other key is the adapter's own (data = "~/.tracker").
type Integration struct {
	Path   string
	Values map[string]string
}

// Value is one of the adapter's keys, with a leading ~/ expanded.
func (i Integration) Value(key string) string {
	v := i.Values[key]
	if rest, ok := strings.CutPrefix(v, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return v
}

// Integration is [integrations.<name>]; the zero value when it is unset.
func (p *Prefs) Integration(name string) Integration {
	if p == nil {
		return Integration{}
	}
	return p.integrations[name]
}

// IntegrationNames lists the configured [integrations.<name>] sections.
func (p *Prefs) IntegrationNames() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.integrations))
	for n := range p.integrations {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func applyIntegration(sec map[string]conf.Value) Integration {
	in := Integration{Values: map[string]string{}}
	for k, v := range sec {
		s := strings.TrimSpace(fmt.Sprint(v))
		if k == "path" {
			in.Path = s
			continue
		}
		in.Values[k] = s
	}
	return in
}

// Ext is an [ext.<name>] section (ADR-127): an extension's own settings,
// every key in Values, path too. The zero value when it is unset.
func (p *Prefs) Ext(name string) Integration {
	if p == nil {
		return Integration{}
	}
	return p.ext[name]
}

func applyExt(sec map[string]conf.Value) Integration {
	in := Integration{Values: map[string]string{}}
	for k, v := range sec {
		in.Values[k] = strings.TrimSpace(fmt.Sprint(v))
	}
	return in
}

// The companion lookup reads config.toml on each call, so an edit applies
// without a restart, as the other sections do.
func init() {
	companion.Config = func(name string) string {
		p, err := Load()
		if err != nil {
			return ""
		}
		return p.Integration(name).Path
	}
}
