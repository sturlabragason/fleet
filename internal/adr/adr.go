// Package adr hands out ADR numbers atomically to parallel agents, in
// ~/.fleet/adr-claims.json under its own adr.lock (flock + atomic rename, like
// internal/board). The next free number is max(highest ADR on main, highest
// live claim) + 1; a claim is released when its ADR appears on main, or by
// Release (DESIGN.md §14, ADR-50).
package adr

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sturlabragason/fleet/internal/jsonx"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Claim is one ADR number held by an agent.
type Claim struct {
	N       int       `json:"n"`
	For     string    `json:"for,omitempty"`
	Created time.Time `json:"created"`

	// extra keeps keys this build does not know (ADR-62).
	extra jsonx.Extra
}

// File is the whole adr-claims.json. Seeded records that the hand-written
// claims were considered once, so releasing them never brings them back.
type File struct {
	Seeded bool    `json:"seeded"`
	Claims []Claim `json:"claims"`

	// extra keeps keys this build does not know (ADR-62).
	extra jsonx.Extra
}

// Store reads and writes adr-claims.json under a directory.
type Store struct{ Dir string }

// NewStore returns a store rooted at dir (normally ~/.fleet).
func NewStore(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) path() string { return filepath.Join(s.Dir, "adr-claims.json") }

// errBroken says adr-claims.json did not parse; only update acts on it, under
// the lock, so a concurrent reader can never move aside a file being written.
var errBroken = errors.New("adr-claims.json does not parse")

// load reads adr-claims.json without touching it. A missing file is an empty
// store; any other read error is returned rather than guessed at, so a claim
// can't be handed out from a file fleet failed to read.
func (s *Store) load() (*File, error) {
	data, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return &File{}, nil
	}
	if err != nil {
		return nil, err
	}
	f := &File{}
	if err := json.Unmarshal(data, f); err != nil {
		return &File{}, errBroken
	}
	return f, nil
}

// update runs fn on the file under an exclusive lock and saves the result.
func (s *Store) update(fn func(*File) error) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(s.Dir, "adr.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	f, err := s.load()
	if errors.Is(err, errBroken) {
		logx.For("adr").Error("broken adr-claims.json, moved to adr-claims.json.broken, starting empty", "path", s.path())
		_ = os.Rename(s.path(), s.path()+".broken")
	} else if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(f, "", "  ")
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path())
}

// pendingSeed is the hand-written claims this file has not recorded yet, and
// nothing once it has, or in a repo the seed does not apply to. The flag is set
// only together with the claims, so a first claim in another repo cannot
// silently disable the seed for fleet's own repo.
func (f *File) pendingSeed(onMain map[int]bool) []Claim {
	if f.Seeded {
		return nil
	}
	return seedFor(onMain)
}

// live drops claims whose ADR already landed on main and sorts the rest.
func live(cs []Claim, onMain map[int]bool) []Claim {
	out := []Claim{}
	for _, c := range cs {
		if !onMain[c.N] {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

// highest returns the largest ADR number on main (0 if none).
func highest(onMain map[int]bool) int {
	n := 0
	for k := range onMain {
		if k > n {
			n = k
		}
	}
	return n
}

// Claim takes the next free number for agent and records it. onMain is the set
// of ADR numbers already in DESIGN.md on main (see OnMain). Claims that landed
// on main are kept in the file and filtered on read: the store is shared by
// every repo, so another repo's DESIGN.md must never delete a live claim.
func (s *Store) Claim(agent string, onMain map[int]bool) (int, error) {
	n := 0
	err := s.update(func(f *File) error {
		if seeds := f.pendingSeed(onMain); len(seeds) > 0 {
			f.Seeded, f.Claims = true, append(f.Claims, seeds...)
		}
		n = highest(onMain)
		for _, c := range live(f.Claims, onMain) {
			if c.N > n {
				n = c.N
			}
		}
		n++
		f.Claims = append(f.Claims, Claim{N: n, For: agent, Created: time.Now()})
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// List returns the live claims, lowest number first.
func (s *Store) List(onMain map[int]bool) ([]Claim, error) {
	f, err := s.load()
	if errors.Is(err, errBroken) {
		return nil, fmt.Errorf("%w (run fleet adr claim to start a fresh one)", err)
	}
	if err != nil {
		return nil, err
	}
	return live(append(f.Claims, f.pendingSeed(onMain)...), onMain), nil
}

// Release gives a number back. Releasing a number nobody holds is not an error
// (a claim whose ADR landed on main has already released itself).
func (s *Store) Release(n int) error {
	if n <= 0 {
		return fmt.Errorf("bad ADR number %d", n)
	}
	return s.update(func(f *File) error {
		out := []Claim{}
		for _, c := range f.Claims {
			if c.N != n {
				out = append(out, c)
			}
		}
		f.Claims = out
		return nil
	})
}

// claimJSON is Claim without its methods, for jsonx.
type claimJSON Claim

func (x *Claim) UnmarshalJSON(b []byte) error {
	var v claimJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Claim(v), e
	return err
}

func (x Claim) MarshalJSON() ([]byte, error) { return jsonx.Marshal(claimJSON(x), x.extra) }

// fileJSON is File without its methods, for jsonx.
type fileJSON File

func (x *File) UnmarshalJSON(b []byte) error {
	var v fileJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = File(v), e
	return err
}

func (x File) MarshalJSON() ([]byte, error) { return jsonx.Marshal(fileJSON(x), x.extra) }
