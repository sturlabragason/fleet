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
	"strings"
	"time"
)

// Kinds of item. An admiral item is a promise, an ask (waiting on the
// user), a request (the user's, in flight) or a followup (left by a closed
// agent). A queue item was queued to the agent or is a next: from its
// report.
const (
	Promise  = "promise"
	Ask      = "ask"
	Request  = "request"
	Followup = "followup"
	Queued   = "queued"
	Next     = "next"
)

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
}

// ID is the short id the admiral passes to fleet_note done=.
func (i Item) ID() string {
	if len(i.UUID) < 8 {
		return i.UUID
	}
	return i.UUID[:8]
}

// Line is "[id] kind (ctx): description".
func (i Item) Line() string {
	c := ""
	if i.Ctx != "" {
		c = " (" + i.Ctx + ")"
	}
	return fmt.Sprintf("[%s] %s%s: %s", i.ID(), i.Kind, c, i.Description)
}

// Store is a backend. Lists are pending items, oldest first.
type Store interface {
	Add(ctx context.Context, it Item) (Item, error)
	Admiral(ctx context.Context, name string) ([]Item, error)
	Queue(ctx context.Context, agent string) ([]Item, error)
	// Queues counts every agent's pending queue items.
	Queues(ctx context.Context) (map[string]int, error)
	// Done completes the pending item whose id starts with ref.
	Done(ctx context.Context, ref string) (Item, error)
	// Finish completes the agent's Queued items entered before t.
	Finish(ctx context.Context, agent string, t time.Time) (int, error)
	// Hand moves the agent's pending queue to leader's queue, or with no
	// leader to admiral as Followup items.
	Hand(ctx context.Context, agent, leader, admiral string) (int, error)
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

// OneLine is s's first line, spaces squeezed, at most 200 runes.
func OneLine(s string) string {
	s = strings.Join(strings.Fields(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:199]) + "…"
	}
	return s
}
