package theme

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
)

// User themes (ADR-201): a file <name>.toml in the themes directory beside
// config.toml is a built-in palette with some roles replaced, picked like a
// built-in with theme = "<name>":
//
//	base = "lane"          # the built-in to start from; lane when left out
//	label = "My lane"      # optional
//	[overrides]
//	waiting = "#ffd75f"    # any role in Roles, as #rrggbb
var user = map[string]Palette{}

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// floors is the least contrast each role keeps on the palette's own base:
// the drafts' measured numbers rounded down, so a palette or a user theme
// may not regress a role (docs/style.md §4). Idle and Gone recede on purpose
// and are never body text.
var floors = map[string]float64{
	"text": 11, "subtext0": 6, "overlay2": 4.5,
	"working": 4.5, "waiting": 7, "error": 5, "stuck": 4.5,
	"select": 5, "supervisor": 4.5, "waiting_alt": 5,
	"idle": 3.5, "gone": 1.6,
}

// roles maps each role name a theme file may set to its field.
func (p *Palette) roles() map[string]*string {
	return map[string]*string{
		"base": &p.Base, "mantle": &p.Mantle, "crust": &p.Crust,
		"surface0": &p.Surface0, "surface1": &p.Surface1, "surface2": &p.Surface2,
		"text": &p.Text, "subtext0": &p.Subtext0, "overlay2": &p.Overlay2, "overlay1": &p.Overlay1, "overlay0": &p.Overlay0,
		"working": &p.Working, "working_dim": &p.WorkingDim, "waiting": &p.Waiting, "waiting_alt": &p.WaitingAlt,
		"error": &p.Error, "stuck": &p.Stuck, "idle": &p.Idle, "gone": &p.Gone,
		"select": &p.Select, "supervisor": &p.Supervisor,
	}
}

// Roles lists the role names a theme file may set, sorted.
func Roles() []string {
	var p Palette
	return slices.Sorted(maps.Keys(p.roles()))
}

// User lists the user themes LoadDir read, sorted.
func User() []string { return slices.Sorted(maps.Keys(user)) }

// LoadDir reads every *.toml in dir as a user theme, replacing the ones read
// before, and returns each file's problems by theme name. A missing dir is
// no themes. A theme with problems still loads: a bad value keeps the base's
// colour, and a low contrast is drawn as written.
func LoadDir(dir string) (map[string][]string, error) {
	user = map[string]Palette{}
	problems := map[string][]string{}
	es, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return problems, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read themes: %w", err)
	}
	for _, e := range es {
		file, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok || e.IsDir() {
			continue
		}
		name, path := strings.ToLower(file), filepath.Join(dir, e.Name())
		if _, ok := palettes[name]; ok {
			problems[name] = []string{"has the name of a built-in theme, so it is ignored: rename the file"}
			continue
		}
		if _, dup := user[name]; dup {
			problems[name] = append(problems[name], e.Name()+" has the same name as another file, differing only in case: one of them is ignored")
		}
		p, probs, err := readFile(path, name)
		if err != nil {
			problems[name] = append(problems[name], err.Error())
			continue
		}
		user[name] = p
		if len(probs) > 0 {
			problems[name] = append(problems[name], probs...)
		}
	}
	return problems, nil
}

// readFile builds one user theme from its base and overrides.
func readFile(path, name string) (Palette, []string, error) {
	d, err := conf.ParseFile(path)
	if err != nil {
		return Palette{}, nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	var probs []string
	base := conf.Str(d[""], "base")
	if base == "" {
		base = Default
	}
	p, ok := palettes[base]
	if !ok {
		probs = append(probs, fmt.Sprintf("unknown base %q, using %s", base, Default))
		p = palettes[Default]
	}
	p.Name, p.Label = name, conf.Str(d[""], "label")
	if p.Label == "" {
		p.Label = name
	}
	p.Team = slices.Clone(p.Team)
	for k := range d[""] {
		if k != "base" && k != "label" {
			probs = append(probs, fmt.Sprintf("unknown key %q: a theme file has base, label and [overrides]", k))
		}
	}
	for sec := range d {
		if sec != "" && sec != "overrides" {
			probs = append(probs, fmt.Sprintf("unknown section [%s]: roles go under [overrides]", sec))
		}
	}
	probs = append(probs, override(&p, d["overrides"])...)
	return p, append(probs, contrastProblems(p)...), nil
}

// override sets each role the file names and returns what it could not set.
func override(p *Palette, kv map[string]conf.Value) []string {
	var probs []string
	roles := p.roles()
	for _, k := range slices.Sorted(maps.Keys(kv)) {
		v := conf.Str(kv, k)
		field, ok := roles[strings.ToLower(k)]
		switch {
		case !ok:
			msg := fmt.Sprintf("unknown role %q", k)
			if s, isState := states[k]; isState {
				msg += fmt.Sprintf(": %s is a status, drawn with role %q", k, s.Role)
			}
			probs = append(probs, msg)
		case !hexColour.MatchString(v):
			probs = append(probs, fmt.Sprintf("%s: %q is not a #rrggbb colour, kept %s", k, v, *field))
		default:
			*field = strings.ToLower(v)
		}
	}
	return probs
}

// contrastProblems lists the roles below their floor on the palette's base.
func contrastProblems(p Palette) []string {
	var probs []string
	roles := p.roles()
	for _, r := range slices.Sorted(maps.Keys(floors)) {
		if got := Contrast(*roles[r], p.Base); got < floors[r] {
			probs = append(probs, fmt.Sprintf("%s %s is %.1f:1 on base %s, below %.1f:1", r, *roles[r], got, p.Base, floors[r]))
		}
	}
	return probs
}
