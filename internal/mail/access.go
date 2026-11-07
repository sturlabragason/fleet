package mail

import (
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Request asks for access from -> to and returns the resulting state:
// pending, or granted when to already had a pending request towards from.
func (s *Store) Request(from, to, reason string) (string, error) {
	if err := checkPair(from, to); err != nil {
		return "", err
	}
	if from == to {
		return "", fmt.Errorf("can't request access to yourself")
	}
	var out string
	err := s.update(func(st *state) error {
		p := st.find(from, to)
		switch {
		case p != nil && p.State == Granted, p == nil && st.s.implicit(from, to):
			out = Granted
			return nil
		case p != nil && p.State == Pending && p.RequestedBy == to: // mutual
			p.State, p.DecidedBy, p.Decided, st.dirty = Granted, from, time.Now(), true
			out = Granted
			text := fmt.Sprintf("%s and %s asked each other: access granted, you can message each other", to, from)
			for _, a := range []string{from, to} {
				if err := st.notify(a, from, KindGrant, text); err != nil {
					return err
				}
			}
			return nil
		case p != nil && p.State == Pending: // already asked; don't notify again
			out = Pending
			return nil
		}
		a, b := key(from, to)
		np := Pair{A: a, B: b, State: Pending, RequestedBy: from, Reason: reason, Requested: time.Now()}
		if p != nil { // denied before: may ask again
			*p = np
		} else {
			st.pairs = append(st.pairs, np)
		}
		st.dirty, out = true, Pending
		return st.notify(to, from, KindRequest, fmt.Sprintf(
			"%s asks for access to message you. Reason: %s. Grant or deny with fleet_access_respond (or: fleet mail grant|deny %s)",
			from, orNone(reason), from))
	})
	if err == nil {
		logx.For("mail").Info("request", "from", from, "to", to, "state", out)
	}
	return out, err
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none given)"
	}
	return s
}

// Respond grants or denies the pending request from requester, answered by
// its target. The requester can never answer its own request; the user
// decides through Decide.
func (s *Store) Respond(by, requester string, grant bool) error {
	if err := checkPair(by, requester); err != nil {
		return err
	}
	err := s.update(func(st *state) error {
		p := st.find(by, requester)
		if by != requester && p != nil && p.State == Pending && p.RequestedBy == by {
			return fmt.Errorf("only %s or the user can decide this request", requester)
		}
		if by == requester || p == nil || p.State != Pending || p.RequestedBy != requester {
			return fmt.Errorf("no pending request from %s", requester)
		}
		return st.decide(p, by, grant)
	})
	if err == nil {
		logx.For("mail").Info("respond", "by", by, "requester", requester, "grant", grant)
	}
	return err
}

// decide sets a pair to granted or denied and tells the agents concerned.
func (st *state) decide(p *Pair, by string, grant bool) error {
	p.DecidedBy, p.Decided, st.dirty = by, time.Now(), true
	if grant {
		p.State = Granted
		text := fmt.Sprintf("%s granted access between %s and %s: you can message each other", by, p.A, p.B)
		for _, a := range []string{p.A, p.B} {
			if a != by {
				if err := st.notify(a, by, KindGrant, text); err != nil {
					return err
				}
			}
		}
		return nil
	}
	p.State = Denied
	if p.RequestedBy == "" || p.RequestedBy == by {
		return nil
	}
	return st.notify(p.RequestedBy, by, KindDeny, fmt.Sprintf("%s denied your access request to %s", by, p.Other(p.RequestedBy)))
}

// Decide is the user's grant, deny or revoke of any pair. Grant and deny
// create the pair when absent.
func (s *Store) Decide(a, b, action string) error {
	if err := checkPair(a, b); err != nil {
		return err
	}
	if a == b {
		return fmt.Errorf("a pair needs two different agents")
	}
	switch action {
	case "revoke":
		return s.revoke(User, a, b)
	case "grant", "deny":
	default:
		return fmt.Errorf("action must be grant, deny or revoke")
	}
	err := s.update(func(st *state) error {
		p := st.find(a, b)
		if p == nil {
			x, y := key(a, b)
			st.pairs = append(st.pairs, Pair{A: x, B: y})
			p = &st.pairs[len(st.pairs)-1]
		}
		return st.decide(p, User, action == "grant")
	})
	if err == nil {
		logx.For("mail").Info("decide", "a", a, "b", b, "action", action)
	}
	return err
}

// Revoke ends the access between by and other (any state), told to other.
// Where implicit access applies, the pair is kept as denied so it sticks.
func (s *Store) Revoke(by, other string) error {
	if err := checkPair(by, other); err != nil {
		return err
	}
	return s.revoke(by, by, other)
}

func (s *Store) revoke(by, a, b string) error {
	err := s.update(func(st *state) error {
		p := st.find(a, b)
		imp := st.s.implicit(a, b)
		switch {
		case p == nil && !imp:
			return fmt.Errorf("no access between %s and %s", a, b)
		case imp:
			if p == nil {
				x, y := key(a, b)
				st.pairs = append(st.pairs, Pair{A: x, B: y})
				p = &st.pairs[len(st.pairs)-1]
			}
			*p = Pair{A: p.A, B: p.B, State: Denied, DecidedBy: by, Decided: time.Now()}
			st.dirty = true
		default:
			st.drop(p)
		}
		for _, x := range []string{a, b} {
			if x != by {
				if err := st.notify(x, by, KindRevoke, fmt.Sprintf("%s revoked access between %s and %s", by, a, b)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		logx.For("mail").Info("revoke", "by", by, "a", a, "b", b)
	}
	return err
}

func (st *state) drop(p *Pair) {
	for i := range st.pairs {
		if &st.pairs[i] == p {
			st.pairs = append(st.pairs[:i], st.pairs[i+1:]...)
			st.dirty = true
			return
		}
	}
}

// ForgetAgent drops every pair of a closed agent (its mailbox stays) and
// tells former granted peers.
func (s *Store) ForgetAgent(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	return s.update(func(st *state) error {
		var keep []Pair
		var peers []string
		for _, p := range st.pairs {
			if !p.Has(name) {
				keep = append(keep, p)
				continue
			}
			if p.State == Granted {
				peers = append(peers, p.Other(name))
			}
		}
		if len(keep) == len(st.pairs) {
			return nil
		}
		st.pairs, st.dirty = keep, true
		for _, o := range peers {
			if err := st.notify(o, User, KindRevoke, fmt.Sprintf("%s was closed; access between %s and %s revoked", name, name, o)); err != nil {
				return err
			}
		}
		logx.For("mail").Info("forget", "agent", name, "peers", len(peers))
		return nil
	})
}
