package fleet

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/todo"
	"github.com/sturlabragason/fleet/internal/todo/todotest"
)

func TestNextLines(t *testing.T) {
	got := nextLines("done: shipped\nnext: write the docs\n- next: add a test\nnext:\nrisks: none")
	if want := []string{"write the docs", "add a test"}; !slices.Equal(got, want) {
		t.Errorf("nextLines = %q, want %q", got, want)
	}
}

var memTodo *todotest.Mem

func init() {
	Register(Extension{Name: "memtodo", Todo: func() (todo.Store, error) {
		if memTodo == nil {
			return nil, todo.ErrNone
		}
		return memTodo, nil
	}})
}

// ADR-164: a spawn opens the agent's session; a steer the agent has had
// closes on done, a queued tweak it has not had yet refuses the done until
// the agent's stated exception; a close ends the session with its
// outcome and hands the undelivered queued tweaks to the admiral.
func TestQueueGateAndSession(t *testing.T) {
	memTodo = todotest.New()
	t.Cleanup(func() { memTodo = nil })
	f, _, told, _ := gateFleet(t, "pro")
	if _, ok, _ := memTodo.SessionOf(t.Context(), "w"); !ok {
		t.Fatal("spawn opened no session")
	}
	f.Tweak("w", todo.Steer, "use a tree")
	if err := f.Store.Update(func(b *board.Board) error {
		b.Agents["w"].Status, b.Agents["w"].StatusSince = "working", time.Now()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.Tweak("w", todo.Queued, "also add docs")
	note, err := f.Report("w", "done", "shipped")
	if err != nil || !Refused(note) || !strings.Contains(note, "also add docs") || len(*told) != 1 {
		t.Fatalf("open tweak: err %v note %q told %q", err, note, *told)
	}
	if is, _ := memTodo.Queue(t.Context(), "w"); len(is) != 1 || is[0].Kind != todo.Queued {
		t.Fatalf("queue after done: %+v", is)
	}
	if _, err := f.Report("w", "done", "stopped\nqueue: stop\nnext: write the docs"); err != nil || !strings.HasPrefix(lastReport(t, f), "done: ") {
		t.Fatalf("exception: err %v report %q", err, lastReport(t, f))
	}
	f.Tweak("w", todo.Steer, "late steer") // reached the agent: completed at close, not handed (ADR-188)
	if err := f.Close("w"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(memTodo.Ended["w"], "done: stopped") {
		t.Fatalf("outcome %q", memTodo.Ended["w"])
	}
	if is, _ := memTodo.Admiral(t.Context(), board.Main); len(is) != 1 || is[0].Description != "also add docs" || is[0].Kind != todo.Followup {
		t.Fatalf("hand-over: %+v", is)
	}
}

// ADR-184: an item carries the sessions that filed and closed it: the
// admiral's and the agent's, each once.
func TestTodoSessions(t *testing.T) {
	memTodo = todotest.New()
	t.Cleanup(func() { memTodo = nil })
	f, _, _, _ := gateFleet(t, "pro")
	if err := supervisor.Save(f.Home, supervisor.Session{SessionID: "adm00001-x", Profile: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Store.Update(func(b *board.Board) error {
		b.Agents["w"].SessionID = "agt00001-y"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	adm := todo.Contributor{Name: board.Main, Profile: "claude", Session: "adm00001-x"}
	w := todo.Contributor{Name: "w", Profile: "cat", Session: "agt00001-y"}
	it, ok, err := f.AddItem(board.Main, todo.Ask, "which repo?", "w", "")
	if err != nil || !ok || !slices.Equal(it.Sessions, []todo.Contributor{adm, w}) {
		t.Fatalf("admiral item: %+v ok %v err %v", it, ok, err)
	}
	if _, err := f.DoneItem(board.Main, "w", it.ID(), ""); err != nil {
		t.Fatal(err)
	}
	f.Tweak("w", todo.Steer, "use a tree")
	if is, _ := memTodo.Queue(t.Context(), "w"); len(is) != 1 || !slices.Equal(is[0].Sessions, []todo.Contributor{w}) {
		t.Fatalf("tweak: %+v", is)
	}
	if _, err := f.Report("w", "done", "shipped"); err != nil {
		t.Fatal(err)
	}
	if len(memTodo.Closed) != 2 {
		t.Fatalf("closed: %+v", memTodo.Closed)
	}
	for _, c := range memTodo.Closed {
		if !slices.Contains(c.Sessions, w) || len(c.Sessions) != len(slices.Compact(slices.Clone(c.Sessions))) {
			t.Errorf("closed %q sessions %+v", c.Description, c.Sessions)
		}
	}
}

func TestQueueException(t *testing.T) {
	for in, want := range map[string]bool{"done\nqueue: stop": true, "- queue: urgent": true, "done, queue: x": false, "done": false} {
		if got := queueException(in); got != want {
			t.Errorf("queueException(%q) = %v", in, got)
		}
	}
}
