package proc

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The parser for the darwin process table (ps_darwin.go) is here, not in
// the darwin file, so it is tested on every OS.

// lstartLayout is ps's lstart under LC_ALL=C, after its runs of spaces are
// collapsed to one: "Mon Nov 16 07:55:48 2026".
const lstartLayout = "Mon Jan 2 15:04:05 2006"

// psRow is one line of psColumns.
type psRow struct {
	PID, PPID int
	State     string // ps stat: state letter, then flags ("Ss", "R+", "Z")
	TTY       string // "??" when it has none
	RSS       uint64 // bytes
	Ticks     uint64 // user + system CPU time, in 1/100 s
	Start     time.Time
	Path      string // the executable, as ps comm prints it
}

// Comm is the row's command name the way Linux's comm reads: the
// executable's base name, without the "-" a login shell is started with.
func (r psRow) Comm() string { return strings.TrimPrefix(filepath.Base(r.Path), "-") }

// Daemon is Info.Daemon for the row: a session leader with no terminal.
func (r psRow) Daemon() bool { return strings.Contains(r.State[1:], "s") && r.TTY == "??" }

// parsePS reads psColumns output, skipping any line it can't read.
func parsePS(out string) []psRow {
	var rows []psRow
	for line := range strings.SplitSeq(out, "\n") {
		if r, ok := parsePSLine(line); ok {
			rows = append(rows, r)
		}
	}
	return rows
}

func parsePSLine(line string) (psRow, bool) {
	f, rest := cutFields(line, 11)
	if len(f) < 11 || rest == "" {
		return psRow{}, false
	}
	pid, err1 := strconv.Atoi(f[0])
	ppid, err2 := strconv.Atoi(f[1])
	rss, err3 := strconv.ParseUint(f[4], 10, 64)
	ticks, ok := parseCPUTime(f[5])
	start, err4 := time.ParseInLocation(lstartLayout, strings.Join(f[6:11], " "), time.Local)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || !ok || f[2] == "" {
		return psRow{}, false
	}
	return psRow{PID: pid, PPID: ppid, State: f[2], TTY: f[3], RSS: rss * 1024, Ticks: ticks, Start: start, Path: rest}, true
}

// cutFields splits the first n space-separated fields off s and returns
// them with the rest of s, its inner spaces kept.
func cutFields(s string, n int) ([]string, string) {
	var f []string
	for len(f) < n {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return f, ""
		}
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			return append(f, s), ""
		}
		f, s = append(f, s[:i]), s[i:]
	}
	return f, strings.TrimSpace(s)
}

// parseCPUTime reads ps's time column, "[[hh:]mm:]ss.cc" (darwin prints
// minutes past 59 rather than hours), as 1/100 s.
func parseCPUTime(s string) (uint64, bool) {
	whole, frac, _ := strings.Cut(s, ".")
	var secs uint64
	for p := range strings.SplitSeq(whole, ":") {
		v, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return 0, false
		}
		secs = secs*60 + v
	}
	var cs uint64
	if frac != "" {
		frac = (frac + "00")[:2]
		v, err := strconv.ParseUint(frac, 10, 64)
		if err != nil {
			return 0, false
		}
		cs = v
	}
	return secs*100 + cs, true
}
