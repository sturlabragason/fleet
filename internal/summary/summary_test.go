package summary

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fake is a model backend that never leaves the test process.
type fake struct {
	reply  string
	err    error
	calls  int
	prompt string
}

func (f *fake) Summarize(_ context.Context, prompt string) (string, error) {
	f.calls++
	f.prompt = prompt
	if f.err != nil {
		return "", f.err
	}
	if f.reply != "" {
		return f.reply, nil
	}
	return fmt.Sprintf("call %d", f.calls), nil
}

func (f *fake) Name() string { return "fake-model" }

func newSum(t *testing.T, m Model) *Summarizer {
	t.Helper()
	return &Summarizer{Model: m, Dir: t.TempDir(), MinInterval: time.Minute, Now: time.Now}
}

func screen(s string) Input { return Input{Agent: "a1", Screen: s, Status: "working"} }

func TestSameScreenIsCached(t *testing.T) {
	m := &fake{}
	s := newSum(t, m)
	first, err := s.Summarize(context.Background(), screen("building..."))
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached || first.Text != "call 1" || first.Model != "fake-model" {
		t.Fatalf("first = %+v", first)
	}
	again, err := s.Summarize(context.Background(), screen("building..."))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Cached || again.Stale || again.Text != "call 1" {
		t.Fatalf("again = %+v", again)
	}
	if m.calls != 1 {
		t.Fatalf("model called %d times for one screen", m.calls)
	}
}

// The cache is on disk, so a second process (here, a second Summarizer) that
// peeks at an unchanged screen pays nothing either.
func TestCacheSurvivesANewSummarizer(t *testing.T) {
	m := &fake{}
	s := newSum(t, m)
	if _, err := s.Summarize(context.Background(), screen("compiling")); err != nil {
		t.Fatal(err)
	}
	s2 := &Summarizer{Model: m, Dir: s.Dir, MinInterval: time.Minute}
	r, err := s2.Summarize(context.Background(), screen("compiling"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Cached || r.Text != "call 1" || m.calls != 1 {
		t.Fatalf("r = %+v, calls = %d", r, m.calls)
	}
}

func TestRateLimitReusesTheLastSummary(t *testing.T) {
	m := &fake{}
	now := time.Now()
	s := newSum(t, m)
	s.Now = func() time.Time { return now }
	if _, err := s.Summarize(context.Background(), screen("one")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second) // inside MinInterval
	r, err := s.Summarize(context.Background(), screen("two"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stale || r.Text != "call 1" || m.calls != 1 {
		t.Fatalf("rate limit broke: r = %+v, calls = %d", r, m.calls)
	}
	now = now.Add(2 * time.Minute) // past it
	r, err = s.Summarize(context.Background(), screen("two"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stale || r.Cached || r.Text != "call 2" {
		t.Fatalf("after the interval: r = %+v", r)
	}
}

func TestFailOpen(t *testing.T) {
	m := &fake{}
	now := time.Now()
	s := newSum(t, m)
	s.Now = func() time.Time { return now }
	if _, err := s.Summarize(context.Background(), screen("one")); err != nil {
		t.Fatal(err)
	}

	// A broken model falls back to the last summary, marked stale.
	m.err = errors.New("no network")
	now = now.Add(2 * time.Minute)
	r, err := s.Summarize(context.Background(), screen("two"))
	if err != nil || !r.Stale || r.Text != "call 1" {
		t.Fatalf("fallback: r = %+v err = %v", r, err)
	}

	// With nothing cached there is simply no summary, never a panic or a hang.
	empty := &Summarizer{Model: &fake{err: errors.New("boom")}, Dir: t.TempDir()}
	if _, err := empty.Summarize(context.Background(), screen("x")); err == nil {
		t.Fatal("want an error when there is nothing to fall back on")
	}
	none := &Summarizer{Dir: t.TempDir()} // no model configured at all
	if _, err := none.Summarize(context.Background(), screen("x")); err == nil {
		t.Fatal("want an error without a model")
	}
}

func TestBlankScreenNeverCallsTheModel(t *testing.T) {
	m := &fake{}
	s := newSum(t, m)
	if _, err := s.Summarize(context.Background(), screen("   \n\n")); !errors.Is(err, ErrNoSummary) {
		t.Fatalf("err = %v", err)
	}
	if m.calls != 0 {
		t.Fatalf("model called %d times on a blank screen", m.calls)
	}
}

func TestCleanKeepsFiveLines(t *testing.T) {
	got := Clean("one\n\n two \n\nthree\nfour\nfive\nsix\nseven")
	want := "one\n two\nthree\nfour\nfive"
	if got != want {
		t.Fatalf("Clean = %q", got)
	}
	s := newSum(t, &fake{reply: "a\nb\nc\nd\ne\nf\ng"})
	r, err := s.Summarize(context.Background(), screen("x"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(r.Text, "\n")); n != MaxLines {
		t.Fatalf("%d lines: %q", n, r.Text)
	}
}

func TestPromptCarriesScreenMessageAndTask(t *testing.T) {
	m := &fake{}
	s := newSum(t, m)
	in := Input{Agent: "a1", Screen: "go test ./...", LastMessage: "tests are red", Status: "working", Task: "fix the parser"}
	if _, err := s.Summarize(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a1", "go test ./...", "tests are red", "working", "fix the parser", "at most 5 short lines"} {
		if !strings.Contains(m.prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, m.prompt)
		}
	}
	// Only the recent part of a long screen is paid for.
	long := Prompt(Input{Agent: "a1", Screen: strings.Repeat("x", ScreenChars*2) + "END"})
	if len(long) > ScreenChars+2000 || !strings.Contains(long, "END") {
		t.Fatalf("long screen not trimmed to its tail: %d chars", len(long))
	}
}

func TestHashChangesWithTheScreen(t *testing.T) {
	a := Hash(Input{Agent: "a1", Screen: "one", Status: "working"})
	if a != Hash(Input{Agent: "a2", Screen: "one", Status: "working"}) {
		t.Fatal("the hash is of the state, not the name")
	}
	for _, other := range []Input{
		{Screen: "two", Status: "working"},
		{Screen: "one", Status: "waiting"},
		{Screen: "one", Status: "working", LastMessage: "hi"},
	} {
		if Hash(other) == a {
			t.Fatalf("hash collision with %+v", other)
		}
	}
}

func TestCacheFileHoldsTheHash(t *testing.T) {
	s := newSum(t, &fake{})
	in := screen("working away")
	r, err := s.Summarize(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, "a1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "hash: "+Hash(in)) || !strings.Contains(string(b), r.Text) {
		t.Fatalf("cache file:\n%s", b)
	}
}

// Each `fleet peek --summary` is its own process, so the rate limit has to
// live in the cache file, not only in memory.
func TestRateLimitHoldsAcrossProcesses(t *testing.T) {
	m := &fake{}
	now := time.Now()
	dir := t.TempDir()
	first := &Summarizer{Model: m, Dir: dir, MinInterval: time.Minute, Now: func() time.Time { return now }}
	if _, err := first.Summarize(context.Background(), screen("one")); err != nil {
		t.Fatal(err)
	}
	next := &Summarizer{Model: m, Dir: dir, MinInterval: time.Minute, Now: func() time.Time { return now.Add(10 * time.Second) }}
	r, err := next.Summarize(context.Background(), screen("two"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stale || m.calls != 1 {
		t.Fatalf("a fresh process paid again: r = %+v calls = %d", r, m.calls)
	}
	later := &Summarizer{Model: m, Dir: dir, MinInterval: time.Minute, Now: func() time.Time { return now.Add(2 * time.Minute) }}
	if r, err = later.Summarize(context.Background(), screen("two")); err != nil || r.Stale || m.calls != 2 {
		t.Fatalf("past the interval: r = %+v err = %v calls = %d", r, err, m.calls)
	}
}

// A model that fails must not be retried at full price by every new process:
// the attempt itself is recorded.
func TestAFailedCallIsRateLimitedToo(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	m := &fake{err: errors.New("model down")}
	at := func(d time.Duration) *Summarizer {
		return &Summarizer{Model: m, Dir: dir, MinInterval: time.Minute, Now: func() time.Time { return now.Add(d) }}
	}
	if _, err := at(0).Summarize(context.Background(), screen("one")); err == nil {
		t.Fatal("want an error")
	}
	if _, err := at(10*time.Second).Summarize(context.Background(), screen("two")); err == nil {
		t.Fatal("want an error")
	}
	if m.calls != 1 {
		t.Fatalf("a fresh process retried a broken model: %d calls", m.calls)
	}
	if _, err := at(2*time.Minute).Summarize(context.Background(), screen("two")); err == nil || m.calls != 2 {
		t.Fatalf("past the interval: err = %v calls = %d", err, m.calls)
	}
}

func TestNoRateLimit(t *testing.T) {
	m := &fake{}
	now := time.Now()
	s := &Summarizer{Model: m, Dir: t.TempDir(), MinInterval: NoRateLimit, Now: func() time.Time { return now }}
	for i, text := range []string{"one", "two", "three"} {
		if _, err := s.Summarize(context.Background(), screen(text)); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if m.calls != 3 {
		t.Fatalf("min_interval = 0 still held calls back: %d", m.calls)
	}
}
