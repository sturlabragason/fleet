package fleet

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/todo"
	"github.com/sturlabragason/fleet/internal/waits"
)

// Waits is everything waiting on the user, oldest first (ADR-167): agents on
// a prompt, an open question or block, or idle with no report, the admirals'
// unread reports, the todo store's open items and agent queues (ADR-164),
// and what extensions add. A todo store that fails is logged and left out.
func (f *Fleet) Waits(now time.Time) []waits.Item {
	in := waits.Input{Agents: f.Store.Load().Sorted()}
	names := f.AdmiralNames()
	for _, n := range names {
		in.Reports = append(in.Reports, f.Mail().Peek(mail.BoxOf(n))...)
	}
	_, err := TodoDo(func(ctx context.Context, s todo.Store) error {
		for _, n := range names {
			its, err := s.Admiral(ctx, n)
			if err != nil {
				return fmt.Errorf("admiral %s items: %w", n, err)
			}
			in.Todos = append(in.Todos, its...)
		}
		qs, err := s.Queues(ctx)
		if err != nil {
			return fmt.Errorf("queues: %w", err)
		}
		for _, agent := range slices.Sorted(maps.Keys(qs)) {
			its, err := s.Queue(ctx, agent)
			if err != nil {
				return fmt.Errorf("queue of %s: %w", agent, err)
			}
			in.Todos = append(in.Todos, its...)
		}
		return nil
	})
	if err != nil {
		logx.For("waits").Warn("todo store left out", "err", err)
	}
	for _, e := range exts {
		if e.Waits != nil {
			in.Extra = append(in.Extra, e.Waits(f)...)
		}
	}
	return waits.Build(in, now)
}
