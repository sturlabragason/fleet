// Package routes keeps the user's request routes: one card per recurring
// kind of request saying where and how fleet runs it (dir, profile, effort,
// skill, permissions, prompt template). The file is ~/.fleet/routes.toml,
// local and never committed, since it holds private paths. The admiral does
// the matching from the triggers; fleet only stores, lists and applies
// routes (ADR-120).
package routes

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// FileName is the routes file in the fleet home.
const FileName = "routes.toml"

// Path is the routes file for a fleet home.
func Path(home string) string { return filepath.Join(home, FileName) }

// Route is one card. Empty fields are unset: the spawn's own defaults apply.
type Route struct {
	Name        string
	Description string
	Triggers    []string
	Dir         string
	Profile     string
	Effort      string
	Model       string
	Worktree    string // "", "true" or "false"
	Skill       string
	May         []string
	Interactive string // "", "true" or "false"
	Check       string
	// Prompt is the template for the first prompt: {task} is replaced by the
	// spawn's prompt; without {task} the template goes before it.
	Prompt      string
	Updated     string
	LearnedFrom string
	// Extra keeps keys fleet doesn't know (from a hand-edited file), so a
	// save doesn't drop them.
	Extra map[string]conf.Value
}

// Fields are the keys fleet_route set accepts, in file order.
var Fields = []string{"description", "triggers", "dir", "profile", "effort", "model", "worktree", "skill", "may", "interactive", "check", "prompt", "learned_from"}

var listFields = map[string]bool{"triggers": true, "may": true}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidName says whether n can name a route ([route.NAME] in the file).
func ValidName(n string) bool { return nameRE.MatchString(n) }

// Load reads the routes, sorted by name. A missing file is no routes.
func Load(home string) ([]Route, error) {
	f, err := load(home)
	return f.routes, err
}

// file is the whole routes file: the routes and any other sections, kept
// so a save doesn't drop them.
type file struct {
	routes []Route
	rest   conf.Doc
}

func load(home string) (file, error) {
	d, err := conf.ParseFile(Path(home))
	if err != nil {
		return file{}, fmt.Errorf("%s: %w", Path(home), err)
	}
	f := file{rest: conf.Doc{}}
	for sec, m := range d {
		name, ok := strings.CutPrefix(sec, "route.")
		if !ok || name == "" {
			if len(m) > 0 || sec != "" {
				f.rest[sec] = m
			}
			continue
		}
		r := Route{Name: name}
		for k, v := range m {
			if err := r.set(k, v); err != nil {
				if r.Extra == nil {
					r.Extra = map[string]conf.Value{}
				}
				r.Extra[k] = v
			}
		}
		f.routes = append(f.routes, r)
	}
	sort.Slice(f.routes, func(i, j int) bool { return f.routes[i].Name < f.routes[j].Name })
	return f, nil
}

// update runs fn on the file under a lock and saves it, so two admirals'
// changes don't overwrite each other.
func update(home string, fn func(*file) error) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(home, "routes.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := sysx.Flock(lf, sysx.LockEx); err != nil {
		return err
	}
	defer sysx.Flock(lf, sysx.LockUn)
	f, err := load(home)
	if err != nil {
		return err
	}
	if err := fn(&f); err != nil {
		return err
	}
	return save(home, f)
}

// Get returns the route named n.
func Get(home, n string) (Route, error) {
	rs, err := Load(home)
	if err != nil {
		return Route{}, err
	}
	for _, r := range rs {
		if r.Name == n {
			return r, nil
		}
	}
	return Route{}, fmt.Errorf("no route %q (%s)", n, names(rs))
}

func names(rs []Route) string {
	if len(rs) == 0 {
		return "no routes yet: fleet_route set adds one"
	}
	var n []string
	for _, r := range rs {
		n = append(n, r.Name)
	}
	return "routes: " + strings.Join(n, ", ")
}

// Change is one field a Set changed, for the decisions log.
type Change struct{ Field, Old, New string }

// Set creates or updates route n with fields (a value of "" or an empty
// list unsets the field) and saves the file. why goes into learned_from.
// It returns the route and what changed.
func Set(home, n string, fields map[string]conf.Value, why string, now time.Time) (Route, []Change, error) {
	if !ValidName(n) {
		return Route{}, nil, fmt.Errorf("route name %q: use lowercase letters, digits, - and _", n)
	}
	var out Route
	var ch []Change
	err := update(home, func(f *file) error {
		i := -1
		for j := range f.routes {
			if f.routes[j].Name == n {
				i = j
			}
		}
		if i < 0 {
			f.routes = append(f.routes, Route{Name: n})
			i = len(f.routes) - 1
		}
		r := &f.routes[i]
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "updated" {
				return fmt.Errorf("updated is set by fleet")
			}
			old := r.show(k)
			if err := r.set(k, fields[k]); err != nil {
				return err
			}
			delete(r.Extra, k) // a bad hand-written value of k is replaced
			if nw := r.show(k); nw != old {
				ch = append(ch, Change{k, old, nw})
			}
		}
		if why = strings.TrimSpace(why); why != "" {
			r.LearnedFrom = why
		}
		if len(ch) > 0 || why != "" {
			r.Updated = now.Format("2006-01-02")
		}
		out = *r
		return nil
	})
	if err != nil {
		return Route{}, nil, err
	}
	return out, ch, nil
}

// Remove deletes route n and saves the file.
func Remove(home, n string) error {
	return update(home, func(f *file) error {
		for i, r := range f.routes {
			if r.Name == n {
				f.routes = append(f.routes[:i], f.routes[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("no route %q (%s)", n, names(f.routes))
	})
}

func (r *Route) set(k string, v conf.Value) error {
	if listFields[k] {
		var l []string
		switch x := v.(type) {
		case []string:
			l = x
		case string:
			if x != "" {
				l = []string{x}
			}
		default:
			return fmt.Errorf("%s: want a list of strings", k)
		}
		if k == "triggers" {
			r.Triggers = l
		} else {
			r.May = l
		}
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%s: want a string", k)
	}
	s = strings.TrimSpace(s)
	switch k {
	case "description":
		r.Description = s
	case "dir":
		r.Dir = s
	case "profile":
		r.Profile = s
	case "effort":
		if s != "" && s != "light" && s != "standard" && s != "pro" {
			return fmt.Errorf("effort %q: want light, standard or pro", s)
		}
		r.Effort = s
	case "model":
		r.Model = s
	case "worktree", "interactive":
		if s != "" && s != "true" && s != "false" {
			return fmt.Errorf("%s %q: want true or false", k, s)
		}
		if k == "worktree" {
			r.Worktree = s
		} else {
			r.Interactive = s
		}
	case "skill":
		r.Skill = s
	case "check":
		r.Check = s
	case "prompt":
		r.Prompt = s
	case "learned_from":
		r.LearnedFrom = s
	case "updated":
		r.Updated = s
	default:
		return fmt.Errorf("unknown route field %q (fields: %s)", k, strings.Join(Fields, ", "))
	}
	return nil
}

// show is field k as one line, "" when unset.
func (r *Route) show(k string) string {
	switch k {
	case "triggers":
		return strings.Join(r.Triggers, ", ")
	case "may":
		return strings.Join(r.May, ", ")
	}
	v, _ := r.value(k).(string)
	return v
}

func (r *Route) value(k string) conf.Value {
	switch k {
	case "description":
		return r.Description
	case "triggers":
		return r.Triggers
	case "dir":
		return r.Dir
	case "profile":
		return r.Profile
	case "effort":
		return r.Effort
	case "model":
		return r.Model
	case "worktree":
		return r.Worktree
	case "skill":
		return r.Skill
	case "may":
		return r.May
	case "interactive":
		return r.Interactive
	case "check":
		return r.Check
	case "prompt":
		return r.Prompt
	case "updated":
		return r.Updated
	case "learned_from":
		return r.LearnedFrom
	}
	return r.Extra[k]
}

// Show is the whole card, one field per line, for fleet_route show.
func (r Route) Show() string {
	var b strings.Builder
	fmt.Fprintf(&b, "route %s\n", r.Name)
	for _, k := range append(append([]string{}, Fields...), "updated") {
		if v := r.show(k); v != "" {
			fmt.Fprintf(&b, "  %s: %s\n", k, strings.ReplaceAll(v, "\n", "\n    "))
		}
	}
	for _, k := range sortedKeys(r.Extra) {
		fmt.Fprintf(&b, "  %s: %v (not used by fleet)\n", k, r.Extra[k])
	}
	return strings.TrimRight(b.String(), "\n")
}

// Hint is the route's one-line trigger text for the brief: its description,
// else its triggers, cut to max runes.
func (r Route) Hint(max int) string {
	h := r.Description
	if h == "" {
		h = strings.Join(r.Triggers, ", ")
	}
	h = strings.Join(strings.Fields(h), " ")
	if rs := []rune(h); len(rs) > max {
		h = string(rs[:max-1]) + "…"
	}
	return h
}

// Index is the brief's list: one "- name: hint" line per route.
func Index(rs []Route) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "- %s: %s\n", r.Name, r.Hint(80))
	}
	return b.String()
}

// ExpandDir resolves a leading ~ in a route's dir.
func ExpandDir(dir string) string {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, dir[1:])
		}
	}
	return dir
}

// PromptFor fills the route's template with the spawn's prompt and puts the
// skill line first.
func (r Route) PromptFor(task string) string {
	p := task
	switch {
	case r.Prompt == "":
	case strings.Contains(r.Prompt, "{task}"):
		p = strings.ReplaceAll(r.Prompt, "{task}", task)
	case task == "":
		p = r.Prompt
	default:
		p = r.Prompt + "\n\n" + task
	}
	if r.Skill != "" {
		p = strings.TrimSpace("Use the " + r.Skill + " skill for this task.\n\n" + p)
	}
	return p
}

const header = `# fleet request routes (ADR-120). Local only: never commit this file.
# One [route.NAME] per recurring request. The admiral matches triggers itself;
# fleet_spawn route=NAME fills in the rest, explicit arguments win.
# fleet_route set rewrites this file: comments are not kept.
`

func save(home string, f file) error {
	var b strings.Builder
	b.WriteString(header)
	for _, k := range sortedKeys(f.rest[""]) {
		writeKV(&b, k, f.rest[""][k])
	}
	for _, sec := range sortedKeys(f.rest) {
		if sec == "" {
			continue
		}
		fmt.Fprintf(&b, "\n[%s]\n", sec)
		for _, k := range sortedKeys(f.rest[sec]) {
			writeKV(&b, k, f.rest[sec][k])
		}
	}
	for _, r := range f.routes {
		fmt.Fprintf(&b, "\n[route.%s]\n", r.Name)
		for _, k := range append(append([]string{}, Fields...), "updated") {
			writeKV(&b, k, r.value(k))
		}
		for _, k := range sortedKeys(r.Extra) {
			writeKV(&b, k, r.Extra[k])
		}
	}
	tmp, err := os.CreateTemp(home, ".routes-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), Path(home))
}

func writeKV(b *strings.Builder, k string, v conf.Value) {
	switch x := v.(type) {
	case string:
		if x == "" {
			return
		}
		if x == "true" || x == "false" {
			fmt.Fprintf(b, "%s = %s\n", k, x)
			return
		}
		fmt.Fprintf(b, "%s = %s\n", k, quote(x))
	case []string:
		if len(x) == 0 {
			return
		}
		q := make([]string, len(x))
		for i, s := range x {
			q[i] = quote(s)
		}
		fmt.Fprintf(b, "%s = [%s]\n", k, strings.Join(q, ", "))
	case map[string]string:
		var kv []string
		for _, kk := range sortedKeys(x) {
			kv = append(kv, kk+" = "+quote(x[kk]))
		}
		fmt.Fprintf(b, "%s = { %s }\n", k, strings.Join(kv, ", "))
	}
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
