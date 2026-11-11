package board

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sturlabragason/fleet/internal/jsonx"
)

// Closed is what a close keeps of an agent so it can be resumed (ADR-95):
// its last board entry, the team it was in, and when it closed.
type Closed struct {
	Agent Agent     `json:"agent"`
	Team  string    `json:"team,omitempty"`
	At    time.Time `json:"at"`
	extra jsonx.Extra
}

// Closed records are kept for ClosedKeep, at most ClosedMax of them.
const (
	ClosedKeep = 24 * time.Hour
	ClosedMax  = 50
)

// Closed records live in closed.json, not board.json (ADR-113): the
// watcher rewrites the board about once a second and the records were most
// of its bytes. Both files are written under board.lock, so a close that
// moves an agent from one to the other is still one locked update. The
// records are read only when something asks for them, so a refresh that
// never touches them never parses or writes them.

func (s *Store) closedPath() string { return filepath.Join(s.Dir, "closed.json") }

// closedSet returns the records, reading closed.json on first use and
// folding in any a board.json from before ADR-113 (or from an older build
// still running) carries. A closed.json that does not parse is kept aside,
// logged and recorded in b.closedErr, which makes Update refuse to write.
func (b *Board) closedSet() map[string]*Closed {
	if b.closed != nil {
		return b.closed
	}
	b.closed = map[string]*Closed{}
	if b.store != nil {
		data, err := os.ReadFile(b.store.closedPath())
		switch {
		case err == nil:
			if err := json.Unmarshal(data, &b.closed); err != nil {
				b.closedErr = b.store.broken("closed.json", data, err)
				b.closed = map[string]*Closed{}
			} else {
				b.closedOld = data
			}
		case !errors.Is(err, fs.ErrNotExist):
			b.closedErr = fmt.Errorf("read closed.json: %w", err)
		}
		if b.closed == nil {
			b.closed = map[string]*Closed{}
		}
	}
	for n, c := range b.LegacyClosed {
		if have, ok := b.closed[n]; !ok || c.At.After(have.At) {
			b.closed[n] = c
		}
	}
	return b.closed
}

// AddClosed records a closed agent and prunes the old records. The copy
// loses its pane and live state: it is a record, not an agent.
func (b *Board) AddClosed(a *Agent, team string, now time.Time) {
	c := &Closed{Agent: *a, Team: team, At: now}
	c.Agent.PaneID, c.Agent.Exit, c.Agent.Closing, c.Agent.Stuck = "", nil, time.Time{}, nil
	b.closedSet()[a.Name] = c
	b.PruneClosed(now)
}

// ClosedRecord is name's record if it is younger than ClosedKeep.
func (b *Board) ClosedRecord(name string, now time.Time) (*Closed, bool) {
	c, ok := b.closedSet()[name]
	if !ok || now.Sub(c.At) > ClosedKeep {
		return nil, false
	}
	return c, true
}

// AnyClosed is name's record whatever its age.
func (b *Board) AnyClosed(name string) (*Closed, bool) {
	c, ok := b.closedSet()[name]
	return c, ok
}

// DropClosed forgets name's record.
func (b *Board) DropClosed(name string) {
	delete(b.closedSet(), name)
	delete(b.LegacyClosed, name)
}

// ClosedErr is why the records could not be read, if they could not.
func (b *Board) ClosedErr() error {
	b.closedSet()
	return b.closedErr
}

// ClosedRecords is every record, newest first.
func (b *Board) ClosedRecords() []*Closed {
	var out []*Closed
	for _, c := range b.closedSet() {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// PruneClosed drops records older than ClosedKeep, then the oldest past
// ClosedMax.
func (b *Board) PruneClosed(now time.Time) {
	set := b.closedSet()
	var keep []*Closed
	for n, c := range set {
		if now.Sub(c.At) > ClosedKeep {
			delete(set, n)
			continue
		}
		keep = append(keep, c)
	}
	if len(keep) <= ClosedMax {
		return
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].At.After(keep[j].At) })
	for _, c := range keep[ClosedMax:] {
		delete(set, c.Agent.Name)
	}
}

// saveClosed writes closed.json, always synced, if the records were read and
// changed, and reports whether it wrote. Once they are safe there,
// board.json drops its legacy copy.
func (s *Store) saveClosed(b *Board) (bool, error) {
	if b.closed == nil {
		return false, nil
	}
	data, err := json.MarshalIndent(b.closed, "", "  ")
	if err != nil {
		return false, err
	}
	if b.closedOld != nil && bytes.Equal(data, b.closedOld) {
		b.LegacyClosed = nil
		return false, nil
	}
	if b.closedOld == nil && len(b.closed) == 0 {
		b.LegacyClosed = nil
		return false, nil
	}
	if err := writeAtomic(s.closedPath(), data, true); err != nil {
		return false, err
	}
	b.LegacyClosed = nil
	return true, nil
}
