package prefs

import (
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
)

// Update is the [update] section: whether a release install upgrades
// itself, and how often it checks (ADR-114). Read on its own, not by Load,
// so only the watcher pays for it.
type Update struct {
	Auto  bool          // [update] auto, default true
	Every time.Duration // [update] interval, default DefaultUpdateEvery
}

// DefaultUpdateEvery is how often a release install looks for a release.
const DefaultUpdateEvery = 6 * time.Hour

// MinUpdateEvery is the shortest interval honoured: a shorter one is raised
// to it, so a typo can't hammer GitHub.
const MinUpdateEvery = 10 * time.Minute

// DefaultUpdate: on, every six hours.
func DefaultUpdate() Update { return Update{Auto: true, Every: DefaultUpdateEvery} }

// LoadUpdate reads [update] from the config file. A missing file or section
// is the default; a bad value is an error with the default alongside it.
func LoadUpdate() (Update, error) {
	path := Path()
	d, err := conf.ParseFile(path)
	if err != nil {
		return DefaultUpdate(), fmt.Errorf("%s: %w", path, err)
	}
	return applyUpdate(DefaultUpdate(), d["update"], path)
}

func applyUpdate(u Update, sec map[string]conf.Value, path string) (Update, error) {
	if sec == nil {
		return u, nil
	}
	if _, ok := sec["auto"]; ok {
		v, ok := conf.Bool(sec, "auto")
		if !ok {
			return u, fmt.Errorf("%s: [update] auto: want true or false", path)
		}
		u.Auto = v
	}
	if v := strings.TrimSpace(conf.Str(sec, "interval")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return u, fmt.Errorf("%s: [update] interval %q: want a duration such as \"6h\"", path, v)
		}
		u.Every = max(d, MinUpdateEvery)
	}
	return u, nil
}
