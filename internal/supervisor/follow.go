package supervisor

import (
	"github.com/sturlabragason/fleet/internal/sessions"
	"github.com/sturlabragason/fleet/internal/tools"
)

// Follow moves s to the session its tool is writing now: after /clear the
// tool starts a new session in the same dir, and a restart must resume that
// one, not the id fleet launched (ADR-101). Only sessions written since s
// started count, so a fresh reset never falls back to its predecessor.
// changed is false when s is already current or nothing newer is found.
func Follow(p tools.Profile, s Session) (Session, bool) {
	if s.SessionID == "" || s.Dir == "" || s.Started.IsZero() {
		return s, false
	}
	n, ok := sessions.Newest(p, s.Dir, s.Started)
	// The session a reset or /clear left must not win back on a late write.
	if !ok || n.ID == s.SessionID || n.ID == s.Previous {
		return s, false
	}
	s.Previous, s.SessionID = s.SessionID, n.ID
	return s, true
}

// FollowOf applies Follow to admiral name's session.json and saves a change.
// It returns the old and new session ids when it moved.
func FollowOf(home, name string, profiles map[string]tools.Profile) (from, to string, err error) {
	s, ok := LoadOf(home, name)
	if !ok {
		return "", "", nil
	}
	p, ok := profiles[s.Profile]
	if !ok {
		return "", "", nil
	}
	ns, moved := Follow(p, s)
	if !moved {
		return "", "", nil
	}
	return s.SessionID, ns.SessionID, SaveOf(home, name, ns)
}
