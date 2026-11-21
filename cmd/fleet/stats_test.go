package main

import (
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
)

// ADR-187: friction lines are not policy interventions, so fleet stats
// leaves them out.
func TestCountInterventionsSkipsFriction(t *testing.T) {
	evs := []board.Event{{Kind: board.KindStuckWake}, {Kind: board.KindDeliver}, {Kind: board.KindCrash}, {To: "idle"}}
	got := countInterventions(evs, func(e board.Event) string { return e.Kind })
	if len(got) != 1 || got[board.KindStuckWake] != 1 {
		t.Fatalf("counts %v", got)
	}
}
