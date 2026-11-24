package ui

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// stallAfter is how long the event loop may go without handling a message
// before the watchdog calls it stalled. animMsg alone arrives every
// animEvery, so a healthy loop never comes close.
const stallAfter = 10 * time.Second

// loopWatch notices when Update stops running: keys, polls and redraws all
// queue behind it, so the overview looks frozen. The stacks it logs say
// what the loop is blocked on.
type loopWatch struct {
	origin  time.Time    // monotonic base: a suspended laptop does not count as idle
	beat    atomic.Int64 // time since origin when Update last ran, 0 before the first
	stalled bool         // watchdog goroutine only
	since   time.Duration
}

// beat records that the event loop handled a message.
func (w *loopWatch) beatNow() { w.beat.Store(int64(max(time.Since(w.origin), 1))) }

// WatchLoop checks the event loop every interval until ctx ends and logs all
// goroutine stacks once when it stalls, then a line when it runs again.
func (m *Model) WatchLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.watch.check(time.Since(m.watch.origin), stacks)
		}
	}
}

// check compares now (time since origin) with the last beat. It returns
// "stalled" or "recovered" on a change, "" otherwise.
func (w *loopWatch) check(now time.Duration, dump func() string) string {
	last := time.Duration(w.beat.Load())
	if last == 0 {
		return ""
	}
	idle := now - last
	switch {
	case !w.stalled && idle >= stallAfter:
		w.stalled, w.since = true, last
		logx.For("ui").Error("event loop stalled: keys and redraws are not handled",
			"idle", idle.Round(time.Second).String(), "stacks", dump())
		return "stalled"
	case w.stalled && last != w.since:
		w.stalled = false
		logx.For("ui").Warn("event loop running again", "stalled", (last - w.since).Round(time.Second).String())
		return "recovered"
	}
	return ""
}

// stacks returns every goroutine's stack, capped at 1 MiB.
func stacks() string {
	buf := make([]byte, 64<<10)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) || len(buf) >= 1<<20 {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}
