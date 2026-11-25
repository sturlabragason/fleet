package pages

import (
	"strings"
	"time"
)

// Published is when page name's live version went up, from `artifact list`
// (name, visibility, RFC 3339 time, URL; tab-separated). ok is false when
// there is no such page; an unreadable time is the zero time, not absence.
func Published(list, name string) (time.Time, bool) {
	for l := range strings.SplitSeq(list, "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 3 || f[0] != name {
			continue
		}
		t, _ := time.Parse(time.RFC3339, f[2]) // zero on error: still exists
		return t, true
	}
	return time.Time{}, false
}

// Unanswered reads `artifact access-log` (UTC time, email, allow|DENY,
// path; tab-separated) for page name since its publish: who last opened it
// and when, if no answer was sent to that version. A republish voids the
// Submit of such an open tab (ADR-199); once one answer is in, the host
// takes no other for that version anyway.
func Unanswered(log, name string, since time.Time) (who string, at time.Time, ok bool) {
	sent := false
	for l := range strings.SplitSeq(log, "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 4 || f[2] != "allow" {
			continue
		}
		t, err := time.Parse(time.DateTime, f[0])
		if err != nil || t.Before(since) {
			continue
		}
		switch f[3] {
		case "/" + name + "/", "/" + name + "/index.html":
			who, at = f[1], t
		case "/_answer/" + name + " sent":
			sent = true
		}
	}
	return who, at, who != "" && !sent
}
