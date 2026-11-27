package fleet

import (
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// TakeBackBatch backspaces are sent at a time, TakeBackPause apart, when
// fleet takes a wake line back out of an input box: copilot drops many of
// one long burst, and the line left behind blocked every retry (a box that
// never reads empty again). 10 per 150 ms cleared it reliably.
var (
	TakeBackBatch = 10
	TakeBackPause = 150 * time.Millisecond
)

// takeBack deletes n characters from the end of box's input box, paced, and
// then checks the box holds just keep (the user's own text before fleet's
// line; usually ""). Characters that are still there after GuardWait were
// dropped, so it deletes those too, up to three rounds in all. It never
// deletes into keep: only whitespace-free characters past it are counted.
func (f *Fleet) takeBack(box, pane string, n int, keep string) {
	log := logx.For("mail")
	want := squash(keep)
	for range 3 {
		for n > 0 {
			k := min(n, TakeBackBatch)
			if err := f.pressKeys(pane, backspaces(k)...); err != nil {
				log.Warn("wake: could not take the line back", "box", box, "err", err)
				return
			}
			n -= k
			time.Sleep(TakeBackPause)
		}
		got, ok := f.boxSettles(box, pane, want)
		if ok {
			return
		}
		left, found := strings.CutPrefix(got, want)
		if !found || left == "" {
			break // the box changed under fleet (the user typed): leave it
		}
		n = len([]rune(left))
	}
	log.Warn("wake: input box not cleared after taking the line back", "box", box)
}

// boxSettles waits up to GuardWait for box's input to read as want
// (squashed) and returns the last squashed input seen. ok is also true when
// the box can't be read, as there is nothing left to check.
func (f *Fleet) boxSettles(box, pane, want string) (string, bool) {
	end := time.Now().Add(GuardWait)
	for {
		v := f.boxView(box)
		if v.Pane != pane || !v.Box {
			return "", true
		}
		got := squash(v.Input)
		if got == want || time.Now().After(end) {
			return got, got == want
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func backspaces(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "BSpace"
	}
	return keys
}
