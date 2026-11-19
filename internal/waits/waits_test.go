package waits

import (
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
			{UUID: "dddd4444x", Queue: "api", Kind: todo.Queued, Description: "also bump the version", Entry: ago(20 * time.Minute), Ctx: "own"},
		},
		Extra: []Item{{Kind: Thread, Source: "release-notes", Ask: "review the draft", Since: ago(3 * time.Hour)}},
	}
	all := Build(in, now)
	got := Only(all, Waiting)
	want := []struct{ kind, source, ask, ref string }{
		{todo.Ask, "main", "reply to the Teams thread", "bbbb2222"}, // the older duplicate wins
		{Thread, "release-notes", "review the draft", ""},
		{Question, "ui", "Teal or blue?", ""}, // its unread report is the same ask
		{todo.Promise, "main", "reply to the Teams thread", "cccc3333"},
		{Report, "docs", "done: README updated", ""},
		{Queue, "api", "queued: also bump the version", "dddd4444"},
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
	if !got[2].Answerable() || !got[6].Answerable() || got[0].Answerable() || got[4].Answerable() {
		t.Error("only agent prompts and open asks are answerable")
	}
	// The tree's fields: parent is the agent, else the source; context is
	// the item's own, else its agent's.
	if q, p := got[5], got[6]; q.Parent != "api" || q.Ctx != "own" || p.Parent != "api" || p.Ctx != "work" || got[0].Parent != "main" {
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
