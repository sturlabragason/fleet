package mcp

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/todo"
	"github.com/sturlabragason/fleet/internal/todo/todotest"
)

// memStore is the in-memory todo store; nil means none.
var memStore *todotest.Mem

func init() {
	fleet.Register(fleet.Extension{Name: "memtodo", Todo: func() (todo.Store, error) {
		if memStore == nil {
			return nil, todo.ErrNone
		}
		return memStore, nil
	}})
}

func useMemTodo(t *testing.T) {
	t.Helper()
	memStore = todotest.New()
	t.Cleanup(func() { memStore = nil })
}

// Without a store an open item is still a note, and the brief has no
// open-items section (ADR-164).
func TestNoteOpenWithoutStore(t *testing.T) {
	s, _, _ := memServer(t)
	out, isErr := callText(t, s, "fleet_note", map[string]any{"text": "which repo for the fix?", "open": "ask"})
	if isErr || !strings.Contains(out, "notes only") {
		t.Errorf("note: %q", out)
	}
	if out, _ := callText(t, s, "fleet_brief", nil); strings.Contains(out, "Open asks") || !strings.Contains(out, "which repo for the fix?") {
		t.Errorf("brief:\n%s", out)
	}
}

// With a store the user's question survives a handoff as an item the next
// brief lists, until fleet_note done= closes it.
func TestOpenItemsSurviveHandoff(t *testing.T) {
	useMemTodo(t)
	s, _, _ := memServer(t)

	out, isErr := callText(t, s, "fleet_note", map[string]any{"text": "promise: tell the user when web-nav merges"})
	if isErr || !strings.Contains(out, "open task [") {
		t.Fatalf("note: %q", out)
	}
	if _, isErr := callText(t, s, "fleet_handoff", map[string]any{"summary": "mid-batch", "open_items": []string{"request: is the relay down for everyone?"}}); isErr {
		t.Fatal("handoff failed")
	}
	brief, _ := callText(t, s, "fleet_brief", nil)
	for _, want := range []string{"## Open asks / in flight", "promise (work): tell the user when web-nav merges", "request (work): is the relay down for everyone?", "Open items:"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief missing %q:\n%s", want, brief)
		}
	}
	id := regexp.MustCompile(`\[([0-9a-f]{8})\] request`).FindStringSubmatch(brief)
	if id == nil {
		t.Fatalf("no request id in brief:\n%s", brief)
	}
	if out, isErr := callText(t, s, "fleet_note", map[string]any{"done": id[1], "text": "answered: yes"}); isErr || !strings.Contains(out, "closed task") {
		t.Fatalf("done: %q", out)
	}
	brief, _ = callText(t, s, "fleet_brief", nil)
	_, open, _ := strings.Cut(brief, "## Open asks")
	open, _, _ = strings.Cut(open, "## Notes")
	if strings.Contains(open, "request (work): is the relay") || !strings.Contains(brief, "done ["+id[1]+"] request") {
		t.Errorf("after done:\n%s", brief)
	}
}

// ADR-188: a handoff that lists an open item again, by its text or by the
// "[id]" line the brief showed, keeps that item instead of filing a copy,
// and an id that is closed is not filed again.
func TestHandoffItemsIdempotent(t *testing.T) {
	useMemTodo(t)
	s, _, _ := memServer(t)
	out, _ := callText(t, s, "fleet_note", map[string]any{"text": "ask: which repo for the fix?"})
	id := regexp.MustCompile(`open task \[([0-9a-f]{8})\]`).FindStringSubmatch(out)
	if id == nil {
		t.Fatalf("note: %q", out)
	}
	items := []string{"ask: Which  repo for the fix?", "[" + id[1] + "] ask (work): which repo for the fix?", "[deadbeef] request (work): gone", "[ADR-12] promise: write it up"}
	if _, isErr := callText(t, s, "fleet_handoff", map[string]any{"summary": "x", "open_items": items}); isErr {
		t.Fatal("handoff failed")
	}
	if is, _ := memStore.Admiral(t.Context(), s.Me()); len(is) != 2 || is[1].Description != "[ADR-12] promise: write it up" {
		t.Fatalf("items after handoff: %+v", is)
	}
	if brief, _ := callText(t, s, "fleet_brief", nil); !strings.Contains(brief, "[deadbeef] request (work): gone (not open any more") {
		t.Errorf("brief:\n%s", brief)
	}
}

// ADR-188: the brief lists the user's items before followups, the last 72
// hours before older ones, asks before promises and requests, newest first,
// and folds a duplicate into the newest copy's line.
func TestRankItems(t *testing.T) {
	now := time.Now()
	it := func(id, kind, text string, age time.Duration) todo.Item {
		return todo.Item{UUID: id + "0000", Kind: kind, Description: text, Entry: now.Add(-age)}
	}
	in := []todo.Item{
		it("f1", todo.Followup, "steer", time.Hour),
		it("a1", todo.Ask, "old ask", 100*time.Hour),
		it("r1", todo.Request, "req", 2*time.Hour),
		it("a2", todo.Ask, "new ask", time.Hour),
		it("a3", todo.Ask, "New  ask", 3*time.Hour),
		it("p1", todo.Promise, "prom", 5*time.Hour),
	}
	got, dups := rankItems(in, now)
	var ids []string
	for _, i := range got {
		ids = append(ids, i.ID())
	}
	if want := []string{"a20000", "p10000", "r10000", "a10000", "f10000"}; !slices.Equal(ids, want) {
		t.Errorf("order %v, want %v", ids, want)
	}
	if d := dups["a20000"]; !slices.Equal(d, []string{"a30000"}) {
		t.Errorf("dups %v", dups)
	}
}
