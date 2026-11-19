package waits

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/todo"
)

func TestBuild(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	asker := &board.Agent{Name: "ui", Status: "idle", StatusSince: ago(time.Hour),
		Report: &board.Report{State: "question", Summary: "Teal or blue?\nmore", At: ago(2 * time.Hour)}}
	prompt := &board.Agent{Name: "api", Status: "waiting", StatusSince: ago(10 * time.Minute), LastLine: "Allow write? 1. Yes 2. No"}
	busy := &board.Agent{Name: "busy", Status: "working", StatusSince: ago(5 * time.Hour), Goal: "build it"}
	old := &board.Agent{Name: "old", Status: "idle", StatusSince: ago(72 * time.Hour)}
	broke := &board.Agent{Name: "broke", Status: "error"}
	in := Input{
		Ctx:    map[string]string{"api": "work", "busy": "home"},
		Agents: []*board.Agent{prompt, busy, asker, old, broke},
		Reports: []mail.Msg{
			{Kind: mail.KindReport, From: "ui", Text: "question: Teal or blue?", T: ago(2 * time.Hour)},
			{Kind: mail.KindReport, From: "docs", Text: "done: README updated", T: ago(30 * time.Minute)},
			{Kind: "msg", From: "docs", Text: "fyi", T: ago(3 * time.Hour)},
		},
		Todos: []todo.Item{
			{UUID: "aaaa1111x", Admiral: "main", Kind: todo.Ask, Description: "Reply to the  Teams thread", Entry: ago(24 * time.Hour)},
			{UUID: "bbbb2222x", Admiral: "main", Kind: todo.Ask, Description: "reply to the Teams thread", Entry: ago(48 * time.Hour)},
			{UUID: "cccc3333x", Admiral: "main", Kind: todo.Promise, Description: "reply to the Teams thread", Entry: ago(time.Hour)},
			{UUID: "ffff6666x", Admiral: "ops", Kind: todo.Ask, Description: "reply to the Teams thread", Entry: ago(30 * time.Hour)},
			{UUID: "dddd4444x", Queue: "api", Kind: todo.Next, Description: "also bump the version", Entry: ago(20 * time.Minute), Ctx: "own"},
		},
		Extra: []Item{{Kind: Thread, Source: "release-notes", Ask: "review the draft", Since: ago(3 * time.Hour)}},
	}
	all := Build(in, now)
	got := Only(all, Waiting)
	want := []struct{ kind, source, ask, ref string }{
		{todo.Ask, "admiral", "reply to the Teams thread", "bbbb2222"}, // the older duplicate wins
		{todo.Ask, "admiral", "reply to the Teams thread", "ffff6666"}, // another admiral's is its own
		{Thread, "release-notes", "review the draft", ""},
		{Question, "ui", "Teal or blue?", ""}, // its unread report is the same ask
		{Report, "docs", "done: README updated", ""},
		{Queue, "api", "next: also bump the version", "dddd4444"},
		{Prompt, "api", "Allow write? 1. Yes 2. No", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Kind != w.kind || g.Source != w.source || g.Ask != w.ask || g.Ref != w.ref {
			t.Errorf("item %d = %s/%s/%q/%s, want %s/%s/%q/%s", i, g.Kind, g.Source, g.Ask, g.Ref, w.kind, w.source, w.ask, w.ref)
		}
	}
	got = slices.DeleteFunc(got, func(it Item) bool { return it.Admiral == "ops" })
	if !got[2].Answerable() || !got[5].Answerable() || got[0].Answerable() || got[3].Answerable() {
		t.Error("only agent prompts and open asks are answerable")
	}
	// The tree's fields: parent is the agent, else the source; context is
	// the item's own, else its agent's.
	if q, p := got[4], got[5]; q.Parent != "api" || q.Ctx != "own" || p.Parent != "api" || p.Ctx != "work" || got[0].Parent != "admiral" || got[0].Admiral != "main" {
		t.Errorf("parent/ctx: queue %q/%q, prompt %q/%q, ask %q", q.Parent, q.Ctx, p.Parent, p.Ctx, got[0].Parent)
	}
	run, quiet := Only(all, Running), Only(all, Quiet)
	if len(run) != 1 || run[0].Source != "busy" || run[0].Kind != Working || run[0].Ask != "build it" || run[0].Ctx != "home" {
		t.Errorf("running = %+v", run)
	}
	if len(quiet) != 1 || quiet[0].Source != "old" || quiet[0].Ask != "idle" {
		t.Errorf("an agent idle past QuietAfter is quiet: %+v", quiet)
	}
	if o := Only(all, Open); len(o) != 1 || o[0].Kind != "error" {
		t.Errorf("an agent in error with no status time is Open with its status: %+v", o)
	}
}

func TestBuildAnsweredQuestionDrops(t *testing.T) {
	now := time.Now()
	a := &board.Agent{Name: "ui", Status: "working", StatusSince: now, TurnAt: now,
		Report: &board.Report{State: "question", Summary: "Teal?", At: now.Add(-time.Minute)}}
	if got := Only(Build(Input{Agents: []*board.Agent{a}}, now), Waiting); len(got) != 0 {
		t.Fatalf("an answered question still waits: %+v", got)
	}
}

// TestTodoSource: an admiral item's source is the agent it names, else its
// "topic:" prefix, else its context, else "admiral"; never the admiral's
// name, which the admiral field keeps.
func TestTodoSource(t *testing.T) {
	agents := []*board.Agent{{Name: "main", Status: "idle"}, {Name: "waits-sort", Status: "working"}}
	tests := []struct {
		desc, ctx             string
		source, agent, parent string
	}{
		{"merge waits-sort once CI is green", "", "waits-sort", "waits-sort", "waits-sort"},
		{"release: tell main about waits-sort.", "work", "waits-sort", "waits-sort", "waits-sort"},
		{"release notes: push v1.54", "work", "release notes", "", "release notes"},
		{"see http://x.io: then decide", "home", "home", "", "admiral"},
		{"ask main about the deck", "", "admiral", "", "admiral"},
		{"a much longer opening clause than a topic: rest", "", "admiral", "", "admiral"},
	}
	for _, tc := range tests {
		in := Input{Agents: agents, Todos: []todo.Item{{UUID: "eeee5555x", Admiral: "main", Kind: todo.Ask, Ctx: tc.ctx, Description: tc.desc}}}
		var it Item
		for _, x := range Only(Build(in, time.Now()), Waiting) {
			if x.Ref != "" {
				it = x
			}
		}
		if it.Source != tc.source || it.Agent != tc.agent || it.Parent != tc.parent || it.Admiral != "main" {
			t.Errorf("%q: source/agent/parent/admiral = %q/%q/%q/%q, want %q/%q/%q/main", tc.desc, it.Source, it.Agent, it.Parent, it.Admiral, tc.source, tc.agent, tc.parent)
		}
	}
}

// TestAging: tweaks wait on their agent (Running), not the user; followups
// and next: lines age into Stale, "user:" chat lines at once; the same text
// for the same owner is one item whatever its kind (ADR-177).
func TestAging(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	in := Input{Todos: []todo.Item{
		{UUID: "t1", Queue: "api", Kind: todo.Steer, Description: "use the v2 client", Entry: ago(time.Minute)},
		{UUID: "t2", Queue: "api", Kind: todo.Queued, Description: "then bump", Entry: ago(time.Minute)},
		{UUID: "t3", Queue: "api", Kind: todo.Next, Description: "old next", Entry: ago(StaleAfter + time.Minute)},
		{UUID: "t4", Queue: "api", Kind: todo.Next, Description: "new next", Entry: ago(time.Minute)},
		{UUID: "f1", Admiral: "main", Kind: todo.Followup, Description: "old followup", Entry: ago(StaleAfter)},
		{UUID: "f2", Admiral: "main", Kind: todo.Followup, Description: "fresh followup", Entry: ago(time.Hour)},
		{UUID: "f3", Admiral: "main", Kind: todo.Followup, Description: "user: now push", Entry: ago(time.Minute)},
		{UUID: "a1", Admiral: "main", Kind: todo.Ask, Description: "old ask", Entry: ago(100 * time.Hour)},
		{UUID: "p1", Admiral: "main", Kind: todo.Promise, Description: "Old  ask", Entry: ago(time.Hour)},
	}}
	sec := map[string]string{}
	for _, it := range Build(in, now) {
		if _, dup := sec[it.Ref]; dup {
			t.Fatalf("ref %s listed twice", it.Ref)
		}
		sec[it.Ref] = it.Section
	}
	want := map[string]string{"t1": Running, "t2": Running, "t3": Stale, "t4": Waiting,
		"f1": Stale, "f2": Waiting, "f3": Stale, "a1": Waiting}
	for ref, w := range want {
		if sec[ref] != w {
			t.Errorf("%s in %q, want %q", ref, sec[ref], w)
		}
	}
	if _, ok := sec["p1"]; ok {
		t.Error("a promise with the text of an older ask by the same owner is a duplicate")
	}
}

// TestAction: what Enter does with each kind of row, and where it goes.
func TestAction(t *testing.T) {
	for _, c := range []struct {
		name   string
		it     Item
		act    string
		target string
	}{
		{"agent question", Item{Kind: Question, Agent: "ui", Section: Waiting}, ActAnswer, ""},
		{"agent prompt", Item{Kind: Prompt, Agent: "ui", Section: Waiting}, ActAnswer, ""},
		{"report", Item{Kind: Report, Agent: "docs", Section: Waiting}, ActPeek, ""},
		{"thread", Item{Kind: Thread, Ref: "th-1", Section: Waiting}, ActResume, ""},
		{"admiral ask", Item{Kind: todo.Ask, Admiral: "main", Ref: "a1", Section: Waiting}, ActAdmiral, "main"},
		{"other admiral's promise", Item{Kind: todo.Promise, Admiral: "ops", Ref: "p1", Section: Waiting}, ActAdmiral, "ops"},
		{"followup about an agent", Item{Kind: todo.Followup, Admiral: "main", Agent: "api", Ref: "f1", Section: Waiting}, ActAgent, "api"},
		{"stale followup", Item{Kind: todo.Followup, Ref: "f2", Section: Stale}, ActAdmiral, "main"},
		{"next on a queue", Item{Kind: Queue, Agent: "api", Ref: "n1", Section: Waiting}, ActAgent, "api"},
		{"idle, no report", Item{Kind: NoReport, Agent: "api", Section: Waiting}, ActAgent, "api"},
		{"running agent", Item{Kind: Working, Agent: "api", Section: Running}, ActShow, ""},
		{"tweak in flight", Item{Kind: Queue, Agent: "api", Ref: "t1", Section: Running}, ActShow, ""},
	} {
		if a, tg := c.it.Action(), c.it.Target(); a != c.act || tg != c.target {
			t.Errorf("%s: %s → %q, want %s → %q", c.name, a, tg, c.act, c.target)
		}
	}
	ask := Item{Kind: todo.Ask, Admiral: "main", Ref: "a1b2c3d4", Ask: "send the reply", Section: Waiting}
	if got := ask.Task(); !strings.Contains(got, "[a1b2c3d4] ask: send the reply") || !strings.Contains(got, "fleet_note done=a1b2c3d4") {
		t.Errorf("admiral task = %q", got)
	}
	if got := (Item{Kind: NoReport, Agent: "api", Section: Waiting}).Task(); !strings.Contains(got, "fleet_report") {
		t.Errorf("no-report task = %q", got)
	}
}
