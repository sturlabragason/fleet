package adr

import "time"

// handedOut is the ADR numbers the fleet admiral gave out by hand on
// 2026-11-05, before this command existed: eight agents were building at once
// and their numbers were not on main yet. The first claim on a machine records
// them so the new counter cannot re-issue a number an agent is already
// writing. Each one drops out of the list by itself as its ADR lands on main.
var handedOut = []struct {
	N   int
	For string
}{
	{43, "fleet-task-card"},
	{44, "fleet-park"},
	{45, "fleet-stuck"},
	{46, "fleet-spend"},
	{47, "fleet-autotrust"},
	{48, "fleet-merge-cmd"},
	{49, "fleet-status-live"},
	{50, "fleet-adr-claim"},
}

var seedDay = time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)

// fingerprint is an ADR only fleet's own DESIGN.md has. The hand-outs mean
// nothing in another repo, so a first claim there starts from that repo's own
// highest number instead of inheriting eight claims it can never release.
const fingerprint = 42

// seedFor returns the hand-written claims a fresh store starts from, and
// nothing at all unless main is fleet's own DESIGN.md.
func seedFor(onMain map[int]bool) []Claim {
	if !onMain[fingerprint] {
		return nil
	}
	out := make([]Claim, 0, len(handedOut))
	for _, h := range handedOut {
		out = append(out, Claim{N: h.N, For: h.For, Created: seedDay})
	}
	return out
}
