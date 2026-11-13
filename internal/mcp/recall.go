package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/recall"
)

// recall is fleet_recall (ADR-119): a search with a query, the day
// digest without one. Neither calls a model.
func (s *Server) recall(a args) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	b := s.F.Store.Load()
	if strings.TrimSpace(a.Query) == "" {
		days := a.Days
		if days <= 0 {
			days = 1
		}
		return recall.DayFor(ctx, s.F.Home, b, s.F.Mail().Peek(s.box()), time.Now().AddDate(0, 0, -days)), nil
	}
	r := recall.Search(ctx, a.Query, recall.Opts{Home: s.F.Home, Days: a.Days, Limit: a.Limit, Agents: recall.FromBoard(b).Sessions})
	return r.Format(), nil
}
