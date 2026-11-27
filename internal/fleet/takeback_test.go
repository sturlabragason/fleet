package fleet

import (
	"os"
	"testing"
	"time"
)

// Copilot drops backspaces from a long burst. The fake input box here keeps
// at most 10 of one call and loses one more on the first call; the wake line
// must still leave the box once Enter fails, or every retry waits on it.
func TestTakeBackPacedAndVerified(t *testing.T) {
	old := TakeBackPause
	TakeBackPause = time.Millisecond
	t.Cleanup(func() { TakeBackPause = old })
	f, pasted := mailFleet(t)
	a := fakeAdm(f, pasted, new(true))
	press := f.PressKeys
	first, most := true, 0
	f.PressKeys = func(pane string, keys ...string) error {
		if len(keys) == 1 && keys[0] == "Enter" {
			return os.ErrPermission
		}
		if keys[0] == "BSpace" {
			most = max(most, len(keys))
			keys = keys[:min(len(keys), 10)]
			if first {
				first, keys = false, keys[1:]
			}
		}
		return press(pane, keys...)
	}
	f.Report("a", "done", "a long enough report line to need several batches of backspaces")
	if a.box != "" {
		t.Fatalf("box after take-back: %q", a.box)
	}
	if most > TakeBackBatch {
		t.Fatalf("%d backspaces in one burst, want at most %d", most, TakeBackBatch)
	}
}
