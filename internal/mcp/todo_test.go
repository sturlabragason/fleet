package mcp

import (
	"regexp"
	"strings"
	"testing"

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
