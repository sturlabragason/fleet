package spend

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/tools"
)

// timeZero is a modification time no test file can share by accident.
var timeZero = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

func TestCopilotFixture(t *testing.T) {
	got, ok := NewCache().File("testdata/copilot-events.jsonl", Copilot)
	if !ok {
		t.Fatal("copilot fixture not read")
	}
	want := Spend{Calls: 3, Input: 200, Output: 1000, CacheRead: 55004, CacheWrite: 5000,
		AIC: 12.80125, HasAIC: true, Model: "claude-opus-5", Context: 15}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if got.Total() != 61204 {
		t.Fatalf("total %d", got.Total())
	}
	if s := got.Short(); s != "12.8 AIC" {
		t.Fatalf("short %q", s)
	}
	if l := got.Long(); l != "12.8 AIC (3 calls, 61k tokens, 55k cached)" {
		t.Fatalf("long %q", l)
	}
}

// The fixture has two applied compactions and one failed one, and a subagent
// call whose small context must not count as the agent's. Each compaction's
// own call (11 AIC, 147k tokens) is billed and is never a usage_record, so it
// is added, the failed attempt's too (ADR-79).
func TestCopilotCompactions(t *testing.T) {
	src, err := os.ReadFile("testdata/copilot-compaction.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	lines := bytes.SplitAfter(src, []byte("\n"))
	os.WriteFile(path, bytes.Join(lines[:6], nil), 0o600)
	c := NewCache()
	got, _ := c.File(path, Copilot)
	if got.Compactions != 1 || got.Context != 20 || got.Calls != 3 || got.AIC != 14 {
		t.Fatalf("after first compaction: %+v", got)
	}
	os.WriteFile(path, src, 0o600)
	os.Chtimes(path, timeZero, timeZero)
	got, _ = c.File(path, Copilot)
	if got.Compactions != 2 || got.Context != 9 || got.Calls != 3 || got.Model != "claude-opus-5" {
		t.Fatalf("got %+v", got)
	}
	if got.AIC != 36 || got.Input != 3*143000+1500 {
		t.Fatalf("compaction calls not counted: %+v", got)
	}
	if s := got.Short(); s != "36.0 AIC ⟲2" {
		t.Fatalf("short %q", s)
	}
	if l := got.Long(); l != "36.0 AIC ⟲2 (3 calls, 646k tokens, 202k cached, compacted 2×)" {
		t.Fatalf("long %q", l)
	}
	var total Spend
	total.Add(got)
	total.Add(got)
	if total.Compactions != 4 || total.Context != 0 {
		t.Fatalf("total %+v", total)
	}
}

// Claude marks a compaction with a compact_boundary system line; an idle one
// (run while the agent waits, before its prompt cache expires) is not the
// full-context churn ADR-71 counts (ADR-79).
func TestClaudeCompactions(t *testing.T) {
	src, err := os.ReadFile("testdata/claude-transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	b := func(uuid, trigger string) string {
		return `{"type":"system","subtype":"compact_boundary","uuid":"` + uuid + `","content":"Conversation compacted","compactMetadata":{"trigger":"` + trigger + `","preTokens":221266}}` + "\n"
	}
	src = append(src, b("c1", "auto")+b("c2", "idle")+b("c3", "manual")...)
	path := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(path, src, 0o600)
	got, _ := NewCache().File(path, Claude)
	if got.Compactions != 2 || got.Calls != 2 || got.Output != 340 {
		t.Fatalf("got %+v", got)
	}
}

func TestClaudeFixture(t *testing.T) {
	// Both files hold the same session: a reply split over two content-block
	// lines (one message, counted once), a second reply, and an API error
	// with model <synthetic> that is not a call. One has the running total
	// newer Claude Code keeps; the two must agree.
	want := Spend{Calls: 2, Input: 10, Output: 340, CacheRead: 27000, CacheWrite: 3500,
		Uncredited: 30850, Model: "claude-sonnet-5"}
	got, ok := NewCache().File("testdata/claude-transcript.jsonl", Claude)
	if !ok {
		t.Fatal("claude fixture not read")
	}
	if got != want {
		t.Fatalf("with cost-state: got %+v want %+v", got, want)
	}
	old, ok := NewCache().File("testdata/claude-transcript-nocoststate.jsonl", Claude)
	if !ok {
		t.Fatal("older claude fixture not read")
	}
	if old != want {
		t.Fatalf("without cost-state: got %+v want %+v", old, want)
	}
	if s := got.Short(); s != "30k tok" {
		t.Fatalf("short %q", s)
	}
	if l := got.Long(); l != "30k tok (2 calls, 30k tokens, 27k cached)" {
		t.Fatalf("long %q", l)
	}
}

// A duplicate content-block line is still skipped when the two halves land in
// different reads, and a cost-state line keeps overriding the sum.
func TestClaudeIncrementalDedup(t *testing.T) {
	src, err := os.ReadFile("testdata/claude-transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(src, []byte("\n"))
	path := filepath.Join(t.TempDir(), "s.jsonl")
	c := NewCache()
	var written []byte
	for _, l := range lines {
		written = append(written, l...)
		if err := os.WriteFile(path, written, 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(path, time.Now(), time.Now())
		c.File(path, Claude)
	}
	step, _ := c.File(path, Claude)
	whole, _ := NewCache().File(path, Claude)
	if step != whole {
		t.Fatalf("line by line %+v != whole file %+v", step, whole)
	}
	if step.Calls != 2 {
		t.Fatalf("calls %d", step.Calls)
	}
}

func TestMissingAndUnknown(t *testing.T) {
	c := NewCache()
	if _, ok := c.File("testdata/nope.jsonl", Copilot); ok {
		t.Fatal("missing file reported ok")
	}
	if _, ok := c.File("testdata/copilot-events.jsonl", Kind("agy")); ok {
		t.Fatal("unknown kind reported ok")
	}
	if _, ok := c.File("", Copilot); ok {
		t.Fatal("empty path reported ok")
	}
}

// A growing log is read incrementally: only the appended bytes are scanned,
// and a half-written last line is not counted until its newline arrives.
func TestIncremental(t *testing.T) {
	src, err := os.ReadFile("testdata/copilot-events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	half := len(src) / 2
	for src[half] != '\n' {
		half++
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, src[:half+1], 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	first, _ := c.File(path, Copilot)
	if first.Calls == 0 || first.Calls == 3 {
		t.Fatalf("first half has %d calls", first.Calls)
	}
	// Append the rest without its final newline: the last record is pending.
	rest := src[half+1:]
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(rest[:len(rest)-1])
	f.Close()
	pending, _ := c.File(path, Copilot)
	if pending.Calls != 2 {
		t.Fatalf("pending line counted: %d calls", pending.Calls)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.Write([]byte("\n"))
	f.Close()
	full, _ := c.File(path, Copilot)
	whole, _ := NewCache().File(path, Copilot)
	if full != whole {
		t.Fatalf("incremental %+v != whole-file %+v", full, whole)
	}
	if full.Calls != 3 {
		t.Fatalf("calls %d", full.Calls)
	}
}

// A replaced (shorter) file is re-read from the start rather than double-counted.
func TestTruncated(t *testing.T) {
	src, _ := os.ReadFile("testdata/copilot-events.jsonl")
	path := filepath.Join(t.TempDir(), "events.jsonl")
	os.WriteFile(path, src, 0o644)
	c := NewCache()
	if s, _ := c.File(path, Copilot); s.Calls != 3 {
		t.Fatalf("calls %d", s.Calls)
	}
	one := src[:0]
	for i, b := range src {
		if b == '\n' {
			one = src[:i+1]
			break
		}
	}
	os.WriteFile(path, one, 0o644)
	os.Chtimes(path, timeZero, timeZero)
	if s, _ := c.File(path, Copilot); s.Calls != 0 {
		t.Fatalf("after truncation: %d calls", s.Calls)
	}
}

func TestAddAndFormat(t *testing.T) {
	// A fleet of both kinds: credits and tokens are different units, so the
	// total must show both rather than hiding the uncredited agents.
	var mixed Spend
	mixed.Add(Spend{Calls: 3, Input: 200, AIC: 12.8, HasAIC: true})
	mixed.Add(Spend{Calls: 2, Input: 30850, Uncredited: 30850})
	if g := mixed.Short(); g != "12.8 AIC + 30k tok" {
		t.Fatalf("mixed total: %q", g)
	}

	var all Spend
	all.Add(Spend{Calls: 2, Input: 5, Output: 7, AIC: 1.5, HasAIC: true})
	all.Add(Spend{Calls: 1, Input: 3, CacheRead: 2_000_000})
	if all.Calls != 3 || all.Input != 8 || all.AIC != 1.5 || !all.HasAIC {
		t.Fatalf("add: %+v", all)
	}
	for _, c := range []struct {
		n    int64
		want string
	}{{913, "913"}, {2345, "2.3k"}, {41_000, "41k"}, {1_200_000, "1.2M"}, {12_000_000, "12M"}} {
		if g := Tokens(c.n); g != c.want {
			t.Errorf("Tokens(%d) = %q want %q", c.n, g, c.want)
		}
	}
	if g := Credits(9.26); g != "9.3" {
		t.Errorf("Credits(9.26) = %q", g)
	}
	if g := Credits(1325.4); g != "1325" {
		t.Errorf("Credits(1325.4) = %q", g)
	}
	if g := (Spend{}).Short(); g != "" {
		t.Errorf("empty Short = %q", g)
	}
	if g := (Spend{}).Long(); g != "" {
		t.Errorf("empty Long = %q", g)
	}
	if g := (Spend{Calls: 1, Output: 5}).Long(); g != "5 tok (1 call, 5 tokens)" {
		t.Errorf("one call Long = %q", g)
	}
}

func TestSourceByProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "nonexistent.toml"))
	ps, err := tools.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	path, k := Source(ps["copilot"], "SID")
	if k != Copilot || filepath.Base(path) != "events.jsonl" {
		t.Fatalf("copilot source %q %q", path, k)
	}
	if _, k := Source(ps["copilot"], ""); k != "" {
		t.Fatal("empty session id resolved")
	}
	// claude's session file is a glob: nothing matches here, so no source.
	if p, _ := Source(ps["claude"], "SID"); p != "" {
		t.Fatalf("claude matched %q", p)
	}
	if p, _ := Source(ps["agy"], "SID"); p != "" {
		t.Fatalf("agy has no usage records, got %q", p)
	}
}

// A resumed Claude session appends new records past the last running total,
// so the snapshot may only be trusted for what came before it.
func TestSnapshotThenResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	snap := `{"type":"cost-state","totalCostUSD":0.1,"modelUsage":{"claude-sonnet-5":` +
		`{"inputTokens":10,"outputTokens":190,"cacheReadInputTokens":0,"cacheCreationInputTokens":0}}}`
	later := `{"type":"assistant","message":{"id":"msg_L","model":"claude-sonnet-5",` +
		`"usage":{"input_tokens":1000,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`
	if err := os.WriteFile(path, []byte(snap+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	got, _ := c.File(path, Claude)
	if got.Total() != 200 || got.Calls != 0 {
		t.Fatalf("snapshot alone: %+v", got)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(later + "\n")
	f.Close()
	os.Chtimes(path, timeZero, time.Now().Add(time.Second))
	got, _ = c.File(path, Claude)
	if got.Total() != 1200 || got.Calls != 1 {
		t.Fatalf("after resume: %+v, want 1200 tokens in 1 call", got)
	}
	// A cold read of the whole file must agree with the incremental one.
	if cold, _ := NewCache().File(path, Claude); cold.Total() != got.Total() || cold.Calls != got.Calls {
		t.Fatalf("cold %+v != incremental %+v", cold, got)
	}
}

// The same session id can sit under two project directories after the agent
// changes directory; the file written to most recently is the live one.
func TestSourcePicksNewestMatch(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "nonexistent.toml"))
	var paths []string
	for _, proj := range []string{"proj-old", "proj-new"} {
		if err := os.MkdirAll(filepath.Join(data, proj), 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(data, proj, "SID.jsonl")
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	os.Chtimes(paths[0], timeZero, time.Now().Add(-time.Hour))
	os.Chtimes(paths[1], timeZero, time.Now())
	cfg := filepath.Join(home, "tools.toml")
	if err := os.WriteFile(cfg, []byte("[claude]\nsession_path = \""+data+"/*/{id}.jsonl\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", cfg)
	ps, err := tools.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got, k := Source(ps["claude"], "SID"); got != paths[1] || k != Claude {
		t.Fatalf("source %q %q, want %q", got, k, paths[1])
	}
}

// An ACP agent's transcript carries each turn's usage as agy reports it:
// cached tokens apart from input, thoughts inside output (DESIGN §7b, §7d).
func TestACPTranscript(t *testing.T) {
	got, ok := NewCache().File("testdata/acp-transcript.jsonl", ACP)
	if !ok {
		t.Fatal("acp transcript not read")
	}
	want := Spend{Calls: 2, Input: 26729, Output: 121, CacheRead: 41080, Uncredited: 67930}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if s := got.Short(); s != "67k tok" {
		t.Fatalf("short %q", s)
	}
}
