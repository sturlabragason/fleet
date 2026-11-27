package status

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// claude-background.jsonl holds real Claude Code transcript lines, cut down
// and scrubbed: 0 a background shell's launch (b3c53l41y), 1 an MCP call
// moved to the background (ktutnk2c0), 2 and 3 their task-notifications, 4 a
// TaskStop of b3a0ah52y.
func bgFixture(t *testing.T) [][]byte {
	t.Helper()
	b, err := os.ReadFile("testdata/claude-background.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Split(bytes.TrimSpace(b), []byte("\n"))
}

func TestScanBackground(t *testing.T) {
	fx := bgFixture(t)
	pick := func(i ...int) []byte {
		var out []byte
		for _, n := range i {
			out = append(append(out, fx[n]...), '\n')
		}
		return out
	}
	quoted := []byte(`{"type":"api-request-blob","message":{"role":"user","content":[{"type":"text","text":"MCP tool \"x\" is still running after 120s. It was moved to the background as task zz1"}]}}
{"type":"user","toolUseResult":{"stdout":"{\"backgroundTaskId\":\"zz2\"}"}}
{"type":"user","toolUseResult":[{"type":"text","text":"grep: It was moved to the background as task zz3"}]}
`)
	for _, c := range []struct {
		name    string
		data    []byte
		running []string
		want    []string
	}{
		{"shell launched", pick(0), nil, []string{"b3c53l41y"}},
		{"shell and mcp launched", pick(0, 1), nil, []string{"b3c53l41y", "ktutnk2c0"}},
		{"mcp notified", pick(0, 1, 2), nil, []string{"b3c53l41y"}},
		{"both notified", pick(0, 1, 2, 3), nil, nil},
		{"notified after the stop offset", pick(3), []string{"b3c53l41y"}, nil},
		{"task stopped", pick(4), []string{"b3a0ah52y", "b3c53l41y"}, []string{"b3c53l41y"}},
		{"quoted words are not launches", quoted, nil, nil},
		{"no trailing newline", fx[0], nil, []string{"b3c53l41y"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ScanBackground(c.data, c.running); !slices.Equal(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The Stop hook reads the whole transcript; later reads start at its
// offset, so only ends recorded after the stop are applied.
func TestStillBackground(t *testing.T) {
	fx := bgFixture(t)
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, append(fx[0], '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	ids, off := BackgroundAt(path)
	s := State{Status: Idle, At: time.Now(), Background: ids, Transcript: path, TranscriptOff: off}
	if got := StillBackground(s); !slices.Equal(got, []string{"b3c53l41y"}) {
		t.Fatalf("before the end: %q", got)
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fh.Write(append(fx[3], '\n'))
	fh.Close()
	if got := StillBackground(s); len(got) != 0 {
		t.Fatalf("after the end: %q", got)
	}
	if got := StillBackground(State{Background: ids, Transcript: filepath.Join(t.TempDir(), "gone")}); got != nil {
		t.Fatalf("missing transcript: %q", got)
	}
}
