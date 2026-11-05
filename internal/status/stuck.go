package status

import (
	"strconv"
	"strings"
	"time"
)

// The stuck rule (DESIGN §4, ADR-45). An agent is stuck when it is grinding:
// the same error banner has come back after a screen change, or it has been
// working with a frozen screen for too long. Being stuck is a label and a
// message to the admiral, nothing else: fleet never pauses or kills an agent
// for it (the optional interrupt is off by default).

// DefaultStuckAfter is how long a working pane may stay unchanged before the
// agent counts as stuck.
const DefaultStuckAfter = 10 * time.Minute

// ErrorRepeats is how many sightings of the same error banner make an agent
// stuck: the playbook's "same failure twice, stop" rule.
const ErrorRepeats = 2

// ErrorForget is how long an agent must work without an error banner before
// that banner is forgotten. It bounds the error trigger: an agent that failed
// twice, recovered and is making progress goes back to working instead of
// staying red until it next reaches its prompt.
const ErrorForget = 2 * time.Minute

// StuckState is one agent's stuck memory, kept on the board between
// refreshes. nil means nothing worth remembering.
type StuckState struct {
	// Since is when the current stuck episode began; zero = not stuck now.
	Since time.Time `json:"since,omitempty"`
	// Reason is the one-line why, for the tile and the admiral.
	Reason string `json:"reason,omitempty"`
	// Notified records that this episode was reported, so the admiral hears
	// about it once however often Refresh runs, in however many processes.
	Notified bool `json:"notified,omitempty"`
	// ErrLine is the error banner the last refresh matched, ErrSeen how many
	// sightings it has. A banner that merely stays on screen is one failure,
	// not one per refresh: it is counted again only after a refresh that did
	// not read an error at all, which is ErrGap. Screen movement is not
	// enough, because a window-scan tool (ADR-42) reports the same banner
	// for as long as it is anywhere on screen, however much else changes.
	ErrLine string `json:"err_line,omitempty"`
	ErrSeen int    `json:"err_seen,omitempty"`
	// ErrGap records that a refresh since the last sighting read no error,
	// so the next sighting is a new failure rather than the same one.
	ErrGap bool `json:"err_gap,omitempty"`
	// ErrWorking is when the agent started working without that banner, so
	// the memory can be dropped after ErrorForget.
	ErrWorking time.Time `json:"err_working,omitempty"`
}

// Stuck reports whether a stuck episode is running.
func (s *StuckState) Stuck() bool { return s != nil && !s.Since.IsZero() }

// Why is the reason for the current episode ("" = not stuck).
func (s *StuckState) Why() string {
	if !s.Stuck() {
		return ""
	}
	return s.Reason
}

// StuckIn is one refresh's view of an agent for the stuck rule.
type StuckIn struct {
	// Status is what the patterns just read off the screen.
	Status Status
	// Line is the error banner Match returned (Status == Error).
	Line string
	// Changed is whether the screen differs from the last refresh.
	Changed bool
	// Still is how long the screen has been unchanged.
	Still time.Duration
	// After is the no-progress threshold; 0 turns that trigger off.
	After time.Duration
	// Now is the refresh time.
	Now time.Time
}

// NextStuck applies the stuck rule to one refresh and returns the status to
// record and the memory to keep. prev may be nil.
//
// It clears when the screen changes. For the no-progress trigger that is
// literal: a screen that moves resets the clock (the caller keeps it in
// Still). For the error trigger it means the screen stopped showing that
// failure — the agent reached its prompt, is asking me, hit a different
// error, or has been working without that banner for ErrorForget. A working
// screen between two sightings is the retry, not a recovery, which is what
// "twice in a row" means at all.
func NextStuck(prev *StuckState, in StuckIn) (Status, *StuckState) {
	st := in.Status
	if st == Stuck {
		// A refresh that could not read the screen carries the board's own
		// status back in. The triggers below decide again from scratch.
		st = Working
	}
	next := StuckState{}
	if prev != nil {
		next = *prev
	}
	switch {
	case st == Error && in.Line != "":
		switch {
		case next.ErrLine != in.Line:
			next.ErrLine, next.ErrSeen = in.Line, 1
		case next.ErrGap:
			next.ErrSeen++
		}
		next.ErrGap, next.ErrWorking = false, time.Time{}
	case st == Working || st == Unknown:
		// The retry between two sightings of the same banner. Forgetting it
		// here would mean nothing ever counted twice, because an agent
		// always does something between two failures — but the memory has
		// to expire, or one that recovered would stay red for good.
		next.ErrGap = true
		if next.ErrWorking.IsZero() {
			next.ErrWorking = in.Now
		}
		if in.Now.Sub(next.ErrWorking) >= ErrorForget {
			next = StuckState{Since: next.Since, Reason: next.Reason, Notified: next.Notified}
		}
	default:
		// Idle or waiting: the agent reached its prompt or is asking me, so
		// it is not grinding on that error any more.
		next.ErrLine, next.ErrSeen, next.ErrGap, next.ErrWorking = "", 0, false, time.Time{}
	}
	var reason string
	switch {
	case next.ErrSeen >= ErrorRepeats:
		reason = "the same error " + times(next.ErrSeen) + " in a row: " + next.ErrLine
	case st == Working && in.After > 0 && in.Still >= in.After:
		reason = "working with no screen change for " + shortDur(in.After)
	}
	if reason == "" {
		if next.ErrLine == "" {
			return st, nil
		}
		return st, &StuckState{ErrLine: next.ErrLine, ErrSeen: next.ErrSeen, ErrGap: next.ErrGap, ErrWorking: next.ErrWorking}
	}
	if next.Since.IsZero() {
		next.Since, next.Notified = in.Now, false
	}
	next.Reason = reason
	return Stuck, &next
}

func times(n int) string {
	if n == 2 {
		return "twice"
	}
	return strconv.Itoa(n) + " times"
}

// shortDur drops zero tails: 5m0s -> 5m, 1h0m0s -> 1h.
func shortDur(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
