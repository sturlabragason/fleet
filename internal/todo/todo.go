// Package todo is the shape of the admiral's open items and each agent's
// queue (ADR-164): promises, open asks and the user's requests in flight,
// kept outside the conversation so they survive a handoff. fleet ships no
// backend; a build that has one fills fleet.Extension.Todo. Without one
// nothing is stored and the notes and handoff stay the only record.
package todo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Kinds of item. An admiral item is a promise, an ask (waiting on the
// user), a request (the user's, in flight) or a followup (left by a closed
// agent). A queue item is a tweak to the agent, or a next: from its report.
// A tweak was queued (held until the agent's turn ends), steered (reached
// the running turn, or started one) or typed by the user into the agent's
// pane (prompt). Only tweaks hold up the agent's session (ADR-164).
const (
	Promise  = "promise"
	Ask      = "ask"
	Request  = "request"
	Followup = "followup"
	Queued   = "queued"
	Steer    = "steer"
	Prompt   = "prompt"
	Next     = "next"
	// SessionKind is an agent session's own item (Store.Open).
	SessionKind = "session"
)

// Tweak is whether a queue item of kind k is a tweak: something the agent
// was asked to do in this session, which its done report must have handled.
func Tweak(k string) bool { return k == Queued || k == Steer || k == Prompt }

// Session is one agent session, from spawn (or adopt, or resume) to close:
// the parent of the agent's queue items. Admiral and Leader place it in the
// tree (admiral, then leader for a squad member, then agent); Goal is the
// card goal or the first line of the task.
type Session struct {
	Agent, Admiral, Leader  string
	Goal, Ctx, Dir, Profile string
}

// AdmiralKinds are the kinds fleet_note open= and fleet_handoff take.
var AdmiralKinds = []string{Promise, Ask, Request}

// Item is one open item. Exactly one of Admiral and Queue is set. Ctx is
// the user's context; its names are user config, never fleet defaults.
type Item struct {
	UUID        string
	Description string
	Entry       time.Time
	Ctx         string
	Admiral     string
	Queue       string
	Kind        string
	// Sessions are the sessions that added to the item, oldest first
	// (ADR-184): the admiral or agent that filed it, and those whose
	// reports, queue or done touched it.
	Sessions []Contributor
}

// Contributor is one session that added to an item: an admiral's or an
// agent's name, its tool profile and its session id, which `fleet adopt
// <id>` resumes. Session is "" while the tool has not told fleet its id.
type Contributor struct {
	Name, Profile, Session string
}

// String is "name (profile id8)", the id shortened to what fleet adopt takes.
func (c Contributor) String() string {
	var in []string
	if c.Profile != "" {
		in = append(in, c.Profile)
	}
	if id := c.Session; id != "" {
		in = append(in, id[:min(len(id), 8)])
	}
	if len(in) == 0 {
		return c.Name
	}
	return c.Name + " (" + strings.Join(in, " ") + ")"
}

// Contribute adds c to cs unless it has no name or cs has it already: the
// same name and session, or the same name with no session yet (that one is
// filled in instead). It never writes into cs's array: stores share slices
// between items.
func Contribute(cs []Contributor, c Contributor) []Contributor {
	if c.Name == "" {
		return cs
	}
	for i, o := range cs {
		switch {
		case o.Name != c.Name:
		case o.Session == c.Session, c.Session == "":
			return cs
		case o.Session == "":
			cs = slices.Clone(cs)
			cs[i] = c
			return cs
		}
	}
	return append(slices.Clip(cs), c)
}

// ID is the short id the admiral passes to fleet_note done=.
func (i Item) ID() string {
	if len(i.UUID) < 8 {
		return i.UUID
	}
	return i.UUID[:8]
}

// Line is "[id] kind (ctx): description", then "; sessions: …" when the
// item has any.
func (i Item) Line() string {
	c := ""
	if i.Ctx != "" {
		c = " (" + i.Ctx + ")"
	}
	l := fmt.Sprintf("[%s] %s%s: %s", i.ID(), i.Kind, c, i.Description)
	if len(i.Sessions) > 0 {
		ss := make([]string, len(i.Sessions))
		for k, s := range i.Sessions {
			ss[k] = s.String()
		}
		l += "; sessions: " + strings.Join(ss, ", ")
	}
	return l
}

// Store is a backend. Lists are pending items, oldest first.
type Store interface {
	// Add files it with its Sessions.
	Add(ctx context.Context, it Item) (Item, error)
	Admiral(ctx context.Context, name string) ([]Item, error)
	Queue(ctx context.Context, agent string) ([]Item, error)
	// Queues counts every agent's pending queue items.
	Queues(ctx context.Context) (map[string]int, error)
	// Done completes the pending item whose id starts with ref, adding by
	// to its sessions first.
	Done(ctx context.Context, ref string, by ...Contributor) (Item, error)
	// Hand moves the agent's pending queue to leader's queue, or with no
	// leader to admiral as Followup items.
	Hand(ctx context.Context, agent, leader, admiral string) (int, error)
	// Open starts the agent's session; a session already open for the
	// agent is kept. Add puts the agent's queue items under it.
	Open(ctx context.Context, s Session) (Item, error)
	// SessionOf is the agent's open session; ok false when it has none.
	SessionOf(ctx context.Context, agent string) (it Item, ok bool, err error)
	// End closes the agent's open session with its outcome (no session:
	// nothing to do).
	End(ctx context.Context, agent, outcome string) error
	// Context is the user's context for work in dir ("" for none).
	Context(dir string) string
}

// ErrNone is what an opener returns when its backend is not available
// (not installed, switched off); Do treats it as "no store", not a failure.
var ErrNone = errors.New("todo: no store")

const timeout = 10 * time.Second

// Do opens a store with open and runs fn under a timeout. No opener, or
// one that returns ErrNone, is ok false with no error.
func Do(open func() (Store, error), fn func(ctx context.Context, s Store) error) (ok bool, err error) {
	if open == nil {
		return false, nil
	}
	s, err := open()
	if err != nil {
		if errors.Is(err, ErrNone) {
			return false, nil // the store is optional (ADR-164)
		}
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return true, fn(ctx, s)
}

// CheckOpen refuses an open kind that is not an admiral kind; "" is none.
func CheckOpen(kind string) error {
	if kind != "" && !slices.Contains(AdmiralKinds, kind) {
		return fmt.Errorf("open must be one of %s", strings.Join(AdmiralKinds, ", "))
	}
	return nil
}

// Kind splits "promise: text" (any AdmiralKinds prefix, any case) into
// its kind and text; text with no such prefix has kind "".
func Kind(text string) (kind, rest string) {
	k, r, ok := strings.Cut(strings.TrimSpace(text), ":")
	if !ok {
		return "", text
	}
	for _, a := range AdmiralKinds {
		if strings.EqualFold(strings.TrimSpace(k), a) {
			return a, strings.TrimSpace(r)
		}
	}
	return "", text
}

// Same is whether two items say the same thing: one kind, and one text
// ignoring case and spacing (ADR-188).
func Same(a, b Item) bool {
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	return a.Kind == b.Kind && norm(a.Description) == norm(b.Description)
}

// OneLine is s's first line, spaces squeezed, at most 200 runes.
func OneLine(s string) string {
	s = strings.Join(strings.Fields(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:199]) + "…"
	}
	return s
}
