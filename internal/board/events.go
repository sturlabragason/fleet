package board

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// EventsFile is the event log in the fleet home; EventsFile+".1" is the
// previous one, kept until the next rotation (ADR-154).
const EventsFile = "events.jsonl"

// EventKeep is how old the first line of events.jsonl may get before the
// watcher rotates it, as usage.Keep for the usage ledger. The rotated file
// is kept for one more period, so a reader sees between one and two.
const EventKeep = 40 * 24 * time.Hour

// Intervention kinds: each is one place fleet's own policy acts on an
// agent (ADR-154). A line with no kind is a status transition.
const (
	KindStopBlock      = "stop_block"      // Stop hook blocked: report owed (ADR-105)
	KindNoReportWake   = "noreport_wake"   // owner woken: idle with no report (ADR-68)
	KindStuckWake      = "stuck_wake"      // owner woken: stuck episode (ADR-45)
	KindRecycleWake    = "recycle_wake"    // owner woken: recycle threshold crossed
	KindResurfaceWake  = "resurface_wake"  // owner woken: ask waited too long
	KindSendRefused    = "send_refused"    // send refused: the user is in the pane (ADR-61)
	KindConfirmRefused = "confirm_refused" // tool call refused without confirmed=true
	KindModelRefused   = "model_refused"   // spawn refused: model not offered
	KindDoneCheck      = "donecheck"       // done-check ran; detail pass or fail (ADR-43)
	KindContextWarning = "context_warning" // admiral context warning appended (ADR-93)
	KindStatusDisagree = "status_disagree" // hook state and screen differ (ADR-49)
)

// Kinds lists every intervention kind.
var Kinds = []string{
	KindStopBlock, KindNoReportWake, KindStuckWake, KindRecycleWake, KindResurfaceWake,
	KindSendRefused, KindConfirmRefused, KindModelRefused, KindDoneCheck,
	KindContextWarning, KindStatusDisagree,
}

// detailMax bounds Detail, so a line stays far under PIPE_BUF and an
// O_APPEND write of it is never split or interleaved (ADR-154).
const detailMax = 200

// Event is one line of events.jsonl: a status transition (From, To) or,
// with Kind set, an intervention. It never holds prompt, report or screen
// text.
type Event struct {
	Time    time.Time   `json:"t"`
	Agent   string      `json:"agent"`
	From    string      `json:"from,omitempty"`
	To      string      `json:"to,omitempty"`
	Kind    string      `json:"kind,omitempty"`
	Profile string      `json:"profile,omitempty"`
	Model   string      `json:"model,omitempty"`
	Detail  string      `json:"detail,omitempty"`
	Pattern *PatternHit `json:"pattern,omitempty"`
}

// PatternHit is which screen pattern decided a status: the profile's
// pattern set, the kind of list (busy, idle, waiting, error, menu, ...) and
// the index in it (-1 for a rule that is not a list entry).
type PatternHit struct {
	Set   string `json:"set"`
	Kind  string `json:"kind"`
	Index int    `json:"index"`
}

// Intervention is an intervention line about a.
func Intervention(now time.Time, a *Agent, kind, detail string) Event {
	return Event{Time: now, Agent: a.Name, Profile: a.Profile, Model: a.Model, Kind: kind, Detail: detail}
}

// AppendEvent appends one line to events.jsonl. Any process may append;
// only the watcher rotates (RotateEvents).
func (s *Store) AppendEvent(e Event) {
	if len(e.Detail) > detailMax {
		e.Detail = e.Detail[:detailMax]
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, EventsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	data, _ := json.Marshal(e)
	_, err = f.Write(append(data, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		logx.For("board").Warn("event not recorded", "agent", e.Agent, "kind", e.Kind, "err", err)
	}
}

// RotateEvents renames events.jsonl to events.jsonl.1 when its first line
// is older than cut, replacing the previous .1. A writer that opened the
// file before the rename finishes its one write into .1, so nothing is
// lost. Only the watcher calls it, so two rotations never race.
func (s *Store) RotateEvents(cut time.Time) (bool, error) {
	path := filepath.Join(s.Dir, EventsFile)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open events: %w", err)
	}
	sc := bufio.NewScanner(f)
	var first Event
	ok := sc.Scan() && json.Unmarshal(sc.Bytes(), &first) == nil
	f.Close()
	if !ok || !first.Time.Before(cut) {
		return false, nil
	}
	if err := os.Rename(path, path+".1"); err != nil {
		return false, fmt.Errorf("rotate events: %w", err)
	}
	return true, nil
}

// ReadEvents returns the lines of events.jsonl.1 and events.jsonl at or
// after since, oldest first. A missing file is no events; an unreadable
// line is skipped.
func (s *Store) ReadEvents(since time.Time) ([]Event, error) {
	var out []Event
	for _, name := range []string{EventsFile + ".1", EventsFile} {
		f, err := os.Open(filepath.Join(s.Dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) == nil && !e.Time.Before(since) {
				out = append(out, e)
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
	}
	return out, nil
}
