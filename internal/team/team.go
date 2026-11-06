// Package team stores named, coloured agent groups in ~/.fleet/teams.json
// (flock + atomic rename, its own teams.lock). One team per agent (DESIGN.md §5c).
package team

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sturlabragason/fleet/internal/jsonx"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/theme"
)

// Team is one named group.
type Team struct {
	Color   string    `json:"color"`
	Members []string  `json:"members"`
	Created time.Time `json:"created"`
	// Squad is the leader whose squad this team mirrors, set only when
	// fleet created the team for it. A team without it is the user's, and
	// fleet never puts a squad into it or deletes it (ADR-59).
	Squad string `json:"squad,omitempty"`

	// extra keeps keys this build does not know (ADR-62).
	extra jsonx.Extra
}

// File is the whole teams.json.
type File struct {
	Teams map[string]*Team `json:"teams"`

	// extra keeps keys this build does not know (ADR-62).
	extra jsonx.Extra
}

// Info is an agent's team and its colour, for renderers.
type Info struct{ Team, Color string }

// Store reads and writes teams.json under a directory.
type Store struct{ Dir string }

// NewStore returns a store rooted at dir.
func NewStore(dir string) *Store { return &Store{Dir: dir} }

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// errSkip makes Update return without writing.
var errSkip = errors.New("no change")

func (s *Store) path() string { return filepath.Join(s.Dir, "teams.json") }

// Load reads teams.json. Broken JSON is logged, moved aside and treated as empty.
func (s *Store) Load() *File {
	f := &File{Teams: map[string]*Team{}}
	data, err := os.ReadFile(s.path())
	if err != nil {
		return f
	}
	if err := json.Unmarshal(data, f); err != nil {
		// Move it aside so the next Load (every poll) doesn't log and back up again.
		logx.For("team").Error("broken teams.json, moved to teams.json.broken, starting empty", "err", err)
		_ = os.Rename(s.path(), s.path()+".broken")
		return &File{Teams: map[string]*Team{}}
	}
	if f.Teams == nil {
		f.Teams = map[string]*Team{}
	}
	return f
}

// Update runs fn on the file under an exclusive lock and saves the result.
func (s *Store) Update(fn func(*File) error) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(s.Dir, "teams.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	f := s.Load()
	if err := fn(f); err != nil {
		if errors.Is(err, errSkip) {
			return nil
		}
		return err
	}
	data, _ := json.MarshalIndent(f, "", "  ")
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path())
}

func checkName(kind, n string) error {
	if !nameRe.MatchString(n) {
		return fmt.Errorf("bad %s name %q", kind, n)
	}
	return nil
}

// ParseColor accepts a palette name (any case) or #rrggbb (lower-cased).
func ParseColor(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, sw := range theme.TeamPalette {
		if strings.EqualFold(s, sw.Name) {
			return sw.Hex, nil
		}
	}
	if len(s) == 7 && s[0] == '#' && strings.Trim(strings.ToLower(s[1:]), "0123456789abcdef") == "" {
		return strings.ToLower(s), nil
	}
	return "", fmt.Errorf("bad colour %q: use #rrggbb or one of %s", s, strings.Join(PaletteNames(), ", "))
}

// PaletteNames lists the palette colour names in order.
func PaletteNames() []string {
	var out []string
	for _, sw := range theme.TeamPalette {
		out = append(out, sw.Name)
	}
	return out
}

// ColorName returns the palette name for a hex colour, or the hex itself.
func ColorName(hex string) string {
	for _, sw := range theme.TeamPalette {
		if strings.EqualFold(sw.Hex, hex) {
			return sw.Name
		}
	}
	return hex
}

// autoColor picks the first palette colour no team uses, else cycles.
func autoColor(f *File) string {
	used := map[string]bool{}
	for _, t := range f.Teams {
		used[strings.ToLower(t.Color)] = true
	}
	for _, sw := range theme.TeamPalette {
		if !used[sw.Hex] {
			return sw.Hex
		}
	}
	return theme.TeamPalette[len(f.Teams)%len(theme.TeamPalette)].Hex
}

// Names returns team names sorted.
func (f *File) Names() []string {
	var out []string
	for n := range f.Teams {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Of returns the team an agent is in ("" if none).
func (f *File) Of(agent string) string {
	for n, t := range f.Teams {
		for _, m := range t.Members {
			if m == agent {
				return n
			}
		}
	}
	return ""
}

func (f *File) get(name string) (*Team, error) {
	t, ok := f.Teams[name]
	if !ok {
		return nil, fmt.Errorf("unknown team %q", name)
	}
	return t, nil
}

func without(l []string, s string) []string {
	out := []string{}
	for _, x := range l {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// Create adds an empty team; color "" picks one automatically.
func (s *Store) Create(name, color string) error {
	if err := checkName("team", name); err != nil {
		return err
	}
	if color != "" {
		c, err := ParseColor(color)
		if err != nil {
			return err
		}
		color = c
	}
	return s.Update(func(f *File) error {
		if _, ok := f.Teams[name]; ok {
			return fmt.Errorf("team %q exists", name)
		}
		if color == "" {
			color = autoColor(f)
		}
		f.Teams[name] = &Team{Color: color, Members: []string{}, Created: time.Now()}
		return nil
	})
}

// Delete removes a team and its memberships; agents are untouched.
func (s *Store) Delete(name string) error {
	return s.Update(func(f *File) error {
		if _, err := f.get(name); err != nil {
			return err
		}
		delete(f.Teams, name)
		return nil
	})
}

// Add puts agents into a team, creating it (auto colour) if missing. Agents in
// another team are moved; moved maps agent to its old team.
func (s *Store) Add(name string, agents ...string) (moved map[string]string, err error) {
	if err := checkName("team", name); err != nil {
		return nil, err
	}
	for _, a := range agents {
		if err := checkName("agent", a); err != nil {
			return nil, err
		}
	}
	moved = map[string]string{}
	err = s.Update(func(f *File) error {
		t, ok := f.Teams[name]
		if !ok {
			t = &Team{Color: autoColor(f), Members: []string{}, Created: time.Now()}
			f.Teams[name] = t
		}
		for _, a := range agents {
			old := f.Of(a)
			if old == name {
				continue
			}
			if old != "" {
				f.Teams[old].Members = without(f.Teams[old].Members, a)
				moved[a] = old
			}
			t.Members = append(t.Members, a)
		}
		return nil
	})
	return moved, err
}

// ErrNotSquad is AddSquad refusing a team that exists but is not leader's
// squad mirror: the user's own team of the same name.
var ErrNotSquad = errors.New("team exists and is not this squad's")

// AddSquad puts agents into leader's squad mirror team, creating it (marked
// as that leader's) if missing. A team of that name that fleet did not make
// for this leader is left untouched and ErrNotSquad is returned.
func (s *Store) AddSquad(name, leader string, agents ...string) error {
	if err := checkName("team", name); err != nil {
		return err
	}
	for _, a := range agents {
		if err := checkName("agent", a); err != nil {
			return err
		}
	}
	return s.Update(func(f *File) error {
		t, ok := f.Teams[name]
		if !ok {
			t = &Team{Color: autoColor(f), Members: []string{}, Created: time.Now(), Squad: leader}
			f.Teams[name] = t
		}
		if t.Squad != leader {
			return fmt.Errorf("%w: %q", ErrNotSquad, name)
		}
		for _, a := range agents {
			old := f.Of(a)
			if old == name {
				continue
			}
			if old != "" {
				f.Teams[old].Members = without(f.Teams[old].Members, a)
			}
			t.Members = append(t.Members, a)
		}
		return nil
	})
}

// DeleteSquad deletes leader's squad mirror team. A missing team, or one
// that is not that leader's mirror, is left alone.
func (s *Store) DeleteSquad(name, leader string) error {
	return s.Update(func(f *File) error {
		t, ok := f.Teams[name]
		if !ok || t.Squad == "" || t.Squad != leader {
			return errSkip
		}
		delete(f.Teams, name)
		return nil
	})
}

// Remove takes agents out of a team.
func (s *Store) Remove(name string, agents ...string) error {
	return s.Update(func(f *File) error {
		t, err := f.get(name)
		if err != nil {
			return err
		}
		for _, a := range agents {
			if f.Of(a) != name {
				return fmt.Errorf("agent %q is not in team %q", a, name)
			}
			t.Members = without(t.Members, a)
		}
		return nil
	})
}

// SetColor changes a team's colour (palette name or #rrggbb).
func (s *Store) SetColor(name, color string) error {
	c, err := ParseColor(color)
	if err != nil {
		return err
	}
	return s.Update(func(f *File) error {
		t, err := f.get(name)
		if err != nil {
			return err
		}
		t.Color = c
		return nil
	})
}

// DropAgent removes an agent from whatever team it is in (no-op if none).
func (s *Store) DropAgent(agent string) error {
	return s.Update(func(f *File) error {
		old := f.Of(agent)
		if old == "" {
			return errSkip
		}
		f.Teams[old].Members = without(f.Teams[old].Members, agent)
		return nil
	})
}

// Of returns an agent's team and colour ("" if none).
func (s *Store) Of(agent string) (team, color string) {
	f := s.Load()
	if n := f.Of(agent); n != "" {
		return n, f.Teams[n].Color
	}
	return "", ""
}

// Members returns a team's members.
func (s *Store) Members(name string) ([]string, error) {
	t, err := s.Load().get(name)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), t.Members...), nil
}

// SameTeam reports whether a and b share a team (false if either has none).
// Agent comms uses it as an implicit two-way grant.
func (s *Store) SameTeam(a, b string) bool {
	f := s.Load()
	ta := f.Of(a)
	return ta != "" && ta == f.Of(b)
}

// Teammates returns the other members of an agent's team.
func (s *Store) Teammates(agent string) []string {
	f := s.Load()
	n := f.Of(agent)
	if n == "" {
		return nil
	}
	return without(f.Teams[n].Members, agent)
}

// ByAgent maps every member to its team and colour.
func (s *Store) ByAgent() map[string]Info {
	out := map[string]Info{}
	for n, t := range s.Load().Teams {
		for _, m := range t.Members {
			out[m] = Info{Team: n, Color: t.Color}
		}
	}
	return out
}

// teamJSON is Team without its methods, for jsonx.
type teamJSON Team

func (x *Team) UnmarshalJSON(b []byte) error {
	var v teamJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Team(v), e
	return err
}

func (x Team) MarshalJSON() ([]byte, error) { return jsonx.Marshal(teamJSON(x), x.extra) }

// fileJSON is File without its methods, for jsonx.
type fileJSON File

func (x *File) UnmarshalJSON(b []byte) error {
	var v fileJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = File(v), e
	return err
}

func (x File) MarshalJSON() ([]byte, error) { return jsonx.Marshal(fileJSON(x), x.extra) }
