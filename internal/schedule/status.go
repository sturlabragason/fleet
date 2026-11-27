package schedule

import (
	"context"
	"time"

	"github.com/sturlabragason/fleet/internal/routes"
)

// grace is how long after a due time a run with no log line counts as missed.
const grace = time.Hour

// Status is one scheduled route as fleet schedule ls and fleet doctor see it.
type Status struct {
	Route string    `json:"route"`
	Spec  string    `json:"schedule"`
	Timer string    `json:"timer"` // active, inactive or missing
	Set   time.Time `json:"-"`     // timer unit mtime: when the schedule was last set
	Times Times     `json:"-"`
	Next  time.Time `json:"next,omitzero"`
	Last  *Entry    `json:"last,omitempty"`
	Err   string    `json:"error,omitempty"` // the calendar did not parse
}

// Statuses reads every route with a schedule.
func Statuses(ctx context.Context, rs []routes.Route, es []Entry, now time.Time) []Status {
	last := Last(es)
	var out []Status
	for _, r := range rs {
		if r.Schedule == "" {
			continue
		}
		s := Status{Route: r.Name, Spec: r.Schedule}
		s.Timer, s.Set = TimerState(ctx, r.Name)
		if w, err := When(ctx, r.Schedule, now); err != nil {
			s.Err = err.Error()
		} else {
			s.Times, s.Next = w, w.Next
		}
		if e, ok := last[r.Name]; ok {
			s.Last = &e
		}
		out = append(out, s)
	}
	return out
}

// Problem is what needs the user's attention, "" when nothing does.
func (s Status) Problem(now time.Time) string {
	switch {
	case s.Err != "":
		return s.Err
	case s.Timer != "active":
		return "timer " + s.Timer
	case s.Last != nil && !s.Last.Manual && s.Last.Outcome != Started && s.Last.At.After(s.Times.Prev.Add(-grace)):
		return "last run " + s.Last.Outcome + ": " + s.Last.Reason
	}
	p := s.Times.Prev
	if p.IsZero() || now.Sub(p) < grace || s.Set.After(p) {
		return ""
	}
	if s.Last == nil || s.Last.At.Before(p.Add(-10*time.Minute)) {
		return "missed the run due " + p.Local().Format(TimeFmt) + ": no log line"
	}
	return ""
}
