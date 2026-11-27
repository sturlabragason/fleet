package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/routes"
	"github.com/sturlabragason/fleet/internal/schedule"
)

// checkSchedules is one line on the scheduled routes (ADR-211): each timer
// runs, and no due run is missing, skipped or failed. No schedules, no line.
func checkSchedules(ctx context.Context, home string) (doctorCheck, bool) {
	rs, err := routes.Load(home)
	if err != nil {
		return doctorCheck{}, false // checkConfig's neighbours report a broken routes file
	}
	es, _ := schedule.Read(home)
	now := time.Now()
	ss := schedule.Statuses(ctx, rs, es, now)
	if len(ss) == 0 {
		return doctorCheck{}, false
	}
	var bad []string
	for _, s := range ss {
		if p := s.Problem(now); p != "" {
			bad = append(bad, s.Route+": "+p)
		}
	}
	if len(bad) == 0 {
		return doctorCheck{Name: "schedules", Level: levelOK, Detail: fmt.Sprintf("%d scheduled routes, timers running, no missed run", len(ss))}, true
	}
	return doctorCheck{Name: "schedules", Level: levelWarn, Detail: strings.Join(bad, "; "),
		Fix: `fleet schedule ls; a missing or stopped timer: fleet schedule add <route> "<calendar>"; a missed run: fleet schedule run <route>`}, true
}
