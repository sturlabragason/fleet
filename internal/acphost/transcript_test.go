package acphost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailAndLastSessionAcrossBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	tx, err := openTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.add(Entry{Kind: KindSession, SessionID: "first"})
	long := strings.Repeat("x", 1000)
	for i := 0; i < 200; i++ { // ~200 KB: several tail blocks
		_ = tx.add(Entry{Kind: KindAgent, Text: fmt.Sprintf("%d %s", i, long)})
	}
	_ = tx.add(Entry{Kind: KindSession, SessionID: "second"})
	_ = tx.add(Entry{Kind: KindAgent, Text: "line one\nlast words"})
	tx.close()

	if got := LastSession(path); got != "second" {
		t.Fatalf("LastSession = %q", got)
	}
	tail := Tail(path, 3)
	if len(tail) != 3 || tail[0].Kind != KindAgent || !strings.HasPrefix(tail[0].Text, "199 ") || tail[1].SessionID != "second" {
		t.Fatalf("Tail = %+v", tail)
	}
	if n := len(Tail(path, 1000)); n != 203 {
		t.Fatalf("Tail(all) = %d entries", n)
	}
	if got := LastLine(path); got != "last words" {
		t.Fatalf("LastLine = %q", got)
	}
	tx, _ = openTranscript(path)
	_ = tx.add(Entry{Kind: KindTool, Title: "Run ls"})
	tx.close()
	if got := LastAgentText(path); got != "line one\nlast words" {
		t.Fatalf("LastAgentText = %q", got)
	}

	// Only the first session in a file whose blocks split lines.
	p2 := filepath.Join(t.TempDir(), "b.jsonl")
	tx2, _ := openTranscript(p2)
	_ = tx2.add(Entry{Kind: KindSession, SessionID: "only"})
	for i := 0; i < 100; i++ {
		_ = tx2.add(Entry{Kind: KindAgent, Text: long})
	}
	tx2.close()
	if got := LastSession(p2); got != "only" {
		t.Fatalf("LastSession = %q", got)
	}
}

func TestTailMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if Tail(filepath.Join(dir, "none"), 5) != nil || LastSession(filepath.Join(dir, "none")) != "" {
		t.Fatal("missing file should give nothing")
	}
	p := filepath.Join(dir, "c.jsonl")
	_ = os.WriteFile(p, []byte("{\"kind\":\"agent\",\"text\":\"ok\"}\nnot json\n"), 0o600)
	if tail := Tail(p, 5); len(tail) != 1 || tail[0].Text != "ok" {
		t.Fatalf("Tail = %+v", tail)
	}
}

func TestRenderIsShort(t *testing.T) {
	cases := map[string]Entry{
		"> hi":               {Kind: KindUser, Text: "hi"},
		"* Read (completed)": {Kind: KindTool, Title: "Read", Status: "completed"},
		"? Run\n  1) Allow\n  2) No\n  type a number and Enter to answer, Esc to cancel the turn": {Kind: KindPermission, Title: "Run", Options: []Option{{Name: "Allow"}, {Name: "No"}}},
		"":               {Kind: KindStatus, Status: "idle"},
		"! status error": {Kind: KindStatus, Status: "error"},
	}
	for want, e := range cases {
		if got := render(e); got != want {
			t.Errorf("render(%+v) = %q, want %q", e, got, want)
		}
	}
	if got := render(Entry{Kind: KindAgent, Text: strings.Repeat("y", 1000)}); len([]rune(got)) != maxLine {
		t.Errorf("long text not clipped: %d", len([]rune(got)))
	}
}
