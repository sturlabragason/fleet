package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/graph"
	"github.com/sturlabragason/fleet/internal/recall"
)

// recall is fleet_recall (ADR-119): a search with a query, the day
// digest without one. Neither calls a model.
func (s *Server) recall(ctx context.Context, a args) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	b := s.F.Store.Load()
	if strings.TrimSpace(a.Query) == "" {
		days := a.Days
		if days <= 0 {
			days = 1
		}
		return recall.DayFor(ctx, s.F.Home, b, s.F.Mail().Peek(s.box()), time.Now().AddDate(0, 0, -days)), nil
	}
	o := recall.Opts{Home: s.F.Home, Days: a.Days, Limit: a.Limit, Agents: recall.FromBoard(b).Sessions}
	if a.Dir != "" {
		if err := absDir(a.Dir); err != nil {
			return "", err
		}
		l := graph.Resolve(s.F.Home, a.Dir, strings.Fields(a.Query))
		o.Repo, o.Files, o.UsedBy = l.Root, l.Files, l.UsedBy
	}
	r := recall.Search(ctx, a.Query, o)
	return r.Format(), nil
}
