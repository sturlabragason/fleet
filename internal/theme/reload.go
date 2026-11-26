package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// Reloader re-reads the user's theme while fleet runs (ADR-201, live
// reload): when config.toml or a file in the themes directory changes, the
// next Check reloads them and makes the configured theme active. Each long
// running process (overview, watcher, web board) owns one and calls Check
// from the goroutine that owns the palette, on a tick it already has.
type Reloader struct {
	Config string // config.toml, for theme = "..."
	Dir    string // the themes directory
	// Name reads the configured theme name; an error keeps the current
	// theme and is the warning.
	Name func() (string, error)

	started       bool
	seen, applied string
}

// Check looks at the sources: a stat of the config file and of each theme
// file, no parsing. A change is applied once it has held still for one call,
// so an editor's several writes reload once. It reports whether the active
// palette changed, and a problem to show the user, "" when none. A theme
// whose file no longer loads keeps the last good palette: fleet never falls
// back to the default mid-session.
func (r *Reloader) Check() (changed bool, warn string) {
	s := r.stamp()
	if !r.started { // start-up already loaded what is there now
		r.started, r.seen, r.applied = true, s, s
		return false, ""
	}
	if s == r.applied || s != r.seen {
		r.seen = s
		return false, ""
	}
	r.applied = s
	return r.reload()
}

func (r *Reloader) reload() (bool, string) {
	name, err := r.Name()
	if err != nil {
		return false, fmt.Sprintf("theme not reloaded, kept %s: %v", active.Name, err)
	}
	if name = strings.ToLower(strings.TrimSpace(name)); name == "" {
		name = Default
	}
	prev := active
	probs, err := LoadDir(r.Dir)
	if err != nil {
		keepListed(prev)
		return false, fmt.Sprintf("theme not reloaded, kept %s: %v", prev.Name, err)
	}
	p, ok := Get(name)
	if !ok {
		keepListed(prev)
		why := "unknown theme"
		if len(probs[name]) > 0 {
			why = name + ".toml: " + strings.Join(probs[name], "; ")
		}
		return false, fmt.Sprintf("theme %s not loaded (%s), kept %s", name, why, prev.Name)
	}
	warn := ""
	if len(probs[name]) > 0 {
		warn = "theme " + name + ".toml: " + strings.Join(probs[name], "; ")
	}
	if reflect.DeepEqual(p, prev) {
		return false, warn
	}
	apply(p)
	return true, warn
}

// keepListed puts the kept palette back among the user themes when its file
// no longer loads, so the picker still lists it and Esc can return to it.
func keepListed(prev Palette) {
	if _, ok := Get(prev.Name); !ok {
		user[prev.Name] = prev
	}
}

// stamp is the mtime and size of the config file, the themes directory and
// each file in it.
func (r *Reloader) stamp() string {
	var b strings.Builder
	add := func(path string) {
		if fi, err := os.Stat(path); err == nil {
			b.WriteString(path + "@" + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(fi.Size(), 10) + "\n")
		}
	}
	add(r.Config)
	add(r.Dir)
	es, _ := os.ReadDir(r.Dir) // no dir is no theme files
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".toml") {
			add(filepath.Join(r.Dir, e.Name()))
		}
	}
	return b.String()
}
