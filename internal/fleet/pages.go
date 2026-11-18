package fleet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/pages"
)

// pagesEvery is how often the resident poll looks for decision-page
// answers nobody took (ADR-163).
const pagesEvery = time.Minute

// watchPages starts a background look for untaken decision-page answers at
// most once a pagesEvery, never two at once: the artifact host is a network
// call and must not stall the poll.
func (f *Fleet) watchPages(now time.Time) {
	if now.Sub(f.pagesAt) < pagesEvery || !f.pagesBusy.CompareAndSwap(false, true) {
		return
	}
	f.pagesAt = now
	go func() {
		defer f.pagesBusy.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		as, err := pages.Poll(ctx, f.Home, now)
		var ce *companion.Error
		if err != nil && !errors.As(err, &ce) {
			logx.For("pages").Warn("decision pages not fully read", "err", err)
		}
		f.handPageAnswers(as)
	}()
}

// handPageAnswers reports each answer to the admiral and wakes it.
func (f *Fleet) handPageAnswers(as []pages.Answer) {
	for _, a := range as {
		text := PageAnswerText(a)
		m, err := f.Mail().ReportTo(a.Page, mail.Admiral, "question", text)
		if err != nil {
			logx.For("pages").Error("answer not reported", "page", a.Page, "err", err)
			continue
		}
		logx.For("pages").Info("answer handed to the admiral", "page", a.Page, "decision", a.Decision)
		f.wakeBox(mail.Admiral, m.ID, a.Page, "question", ReportLine(a.Page, "question", text))
	}
}

// PageAnswerText is the report for an answer no agent took: what the user
// picked and how to take it, so it is acted on once.
func PageAnswerText(a pages.Answer) string {
	at := time.Unix(int64(a.T), 0).Format("15:04")
	return fmt.Sprintf("answer from page %s (sent %s, nobody took it): %q. Take it with `artifact answer %s` and act on it, or hand it to the agent that published the page", a.Page, at, a.Line, a.Page)
}
