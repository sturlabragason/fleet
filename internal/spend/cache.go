package spend

import (
	"io"
	"os"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/tools"
)

// Cache reads each session file once and then only its new tail. An entry is
// valid while the file's size and modification time are unchanged, so a quiet
// agent costs one Stat; a busy one costs the bytes it has written since the
// last read. Never call this from a TUI draw (§7b).
type Cache struct {
	mu sync.Mutex
	m  map[string]*entry
}

type entry struct {
	size int64     // file size at the last read
	mod  time.Time // modification time at the last read
	off  int64     // bytes already accounted for
	acc  Spend     // records added up so far
	snap Spend     // the tool's own running total, when it keeps one
	at   Spend     // records counted when that total was written
	has  bool      // a snapshot was seen
	last string    // id of the last record counted (duplicate guard)
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{m: map[string]*entry{}} }

// Default is the process-wide cache. Each command and the MCP server share it.
var Default = NewCache()

// maxFirstRead caps the first pass over a session file, so one enormous log
// cannot make the admiral's status call wait. Past it the oldest records are
// skipped and the figure is a floor.
const maxFirstRead int64 = 64 << 20

// File returns the spend recorded in one session file. ok is false when the
// file is missing, unreadable or in a format spend cannot parse.
func (c *Cache) File(path string, k Kind) (Spend, bool) {
	p, ok := parserFor(k)
	if !ok || path == "" {
		return Spend{}, false
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return Spend{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[path]
	if e == nil {
		e = &entry{}
		c.m[path] = e
	}
	if e.size == st.Size() && e.mod.Equal(st.ModTime()) {
		return e.total(), true
	}
	if st.Size() < e.size { // truncated or replaced: start again
		*e = entry{}
	}
	f, err := os.Open(path)
	if err != nil {
		return e.total(), e.size > 0
	}
	defer f.Close()
	off := e.off
	if st.Size()-off > maxFirstRead {
		off = st.Size() - maxFirstRead
		if i := skipPartial(f, off); i >= 0 {
			off = i
		}
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return e.total(), e.size > 0
	}
	r := scan(p, buf, e.last)
	model, ctx := r.add.Model, r.add.Context
	if r.hasSnap {
		at := e.acc
		at.Add(r.atSnap)
		e.snap, e.at, e.has = r.snap, at, true
	}
	e.acc.Add(r.add)
	if model != "" {
		e.acc.Model = model
	}
	if ctx > 0 {
		e.acc.Context = ctx
	}
	e.last = r.lastID
	e.off = off + r.consumed
	e.size, e.mod = st.Size(), st.ModTime()
	return e.total(), true
}

// total is the entry's spend: the tool's own running total when it keeps one
// (it is authoritative for everything up to the point it was written), plus
// the records written after it, which a resumed session appends past the last
// snapshot. The call count and the model always come from the records, which
// no running total carries.
func (e *entry) total() Spend {
	out := e.acc
	if e.has {
		out.Input = e.snap.Input + nonNeg(e.acc.Input-e.at.Input)
		out.Output = e.snap.Output + nonNeg(e.acc.Output-e.at.Output)
		out.CacheRead = e.snap.CacheRead + nonNeg(e.acc.CacheRead-e.at.CacheRead)
		out.CacheWrite = e.snap.CacheWrite + nonNeg(e.acc.CacheWrite-e.at.CacheWrite)
	}
	if !out.HasAIC {
		out.Uncredited = out.Total()
	}
	return out
}

func nonNeg(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

// skipPartial returns the offset of the first whole line at or after off
// (-1 when there is none within one window).
func skipPartial(f *os.File, off int64) int64 {
	probe := make([]byte, 1<<16)
	n, _ := f.ReadAt(probe, off)
	for i := 0; i < n; i++ {
		if probe[i] == '\n' {
			return off + int64(i) + 1
		}
	}
	return -1
}

// Agent returns the spend of one agent's session under its tool profile.
// Profiles are mapped by their status pattern set, so copilot variants such
// as claude-copilot need no extra configuration.
func (c *Cache) Agent(p tools.Profile, sessionID string) (Spend, bool) {
	path, k := Source(p, sessionID)
	if path == "" {
		return Spend{}, false
	}
	return c.File(path, k)
}

// Source is the session file holding an agent's usage and its format
// ("" when the profile records none).
func Source(p tools.Profile, sessionID string) (string, Kind) {
	if sessionID == "" {
		return "", ""
	}
	switch Kind(p.Patterns) {
	case Copilot:
		return p.SubagentLogPath(sessionID), Copilot
	case Claude:
		// The session path is a glob over project directories. The same id
		// can sit under two of them when the agent changed directory, so
		// take the one still being written to.
		if m := newest(p.SessionMatches(sessionID)); m != "" {
			return m, Claude
		}
	}
	return "", ""
}

func newest(paths []string) string {
	best, bestMod := "", time.Time{}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if best == "" || st.ModTime().After(bestMod) {
			best, bestMod = p, st.ModTime()
		}
	}
	return best
}
