package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/theme"
)

func writeEvents(t *testing.T, home, id, body string) {
	t.Helper()
	d := filepath.Join(home, "session-state", id)
	os.MkdirAll(d, 0o755)
	if err := os.WriteFile(filepath.Join(d, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func usage(in, max int) string {
	return fmt.Sprintf(`{"type":"session.usage_record","data":{"usage":{"inputTokens":%d,"maxPromptTokens":%d}}}`+"\n", in, max)
}

func TestFillPicksLastUsageRecord(t *testing.T) {
	old := tailChunk
	tailChunk = 1 << 10
	t.Cleanup(func() { tailChunk = old })
	home := t.TempDir()
	var b strings.Builder
	b.WriteString(usage(1000, 200000))
	for i := 0; i < 2000; i++ { // large prefix: more than one tail window
		fmt.Fprintf(&b, `{"type":"assistant.message","data":{"content":"line %d mentions session.usage_record"}}`+"\n", i)
	}
	writeEvents(t, home, "s1", b.String())
	// the only record is far from the end: the window has to widen
	if u, m, ok := Fill(home, "s1"); !ok || u != 1000 || m != 200000 {
		t.Fatalf("widen: %d %d %v", u, m, ok)
	}
	b.WriteString(usage(68000, 200000))
	b.WriteString(`{"type":"tool.execution_complete","data":{}}` + "\n")
	b.WriteString(usage(74000, 200000))
	b.WriteString(`{"type":"user.message","data":{"content":"hi"}}` + "\n")
	writeEvents(t, home, "s2", b.String())
	if u, m, ok := Fill(home, "s2"); !ok || u != 74000 || m != 200000 {
		t.Fatalf("last: %d %d %v", u, m, ok)
	}
}

func TestFillUnknown(t *testing.T) {
	home := t.TempDir()
	if _, _, ok := Fill(home, "missing"); ok {
		t.Error("missing file must be unknown")
	}
	if _, _, ok := Fill(home, ""); ok {
		t.Error("empty id must be unknown")
	}
	writeEvents(t, home, "s", `{"type":"session.start","data":{}}`+"\n")
	if _, _, ok := Fill(home, "s"); ok {
		t.Error("no usage yet must be unknown")
	}
}

func TestSessionIDFromCommand(t *testing.T) {
	for cmd, want := range map[string]string{
		`bash -lc 'exec copilot --session-id 3f2a91c0-1111-4222-8333-444455556666 --no-custom-instructions'`: "3f2a91c0-1111-4222-8333-444455556666",
		`copilot --session-id=abc`:                   "abc",
		`bash -lc 'exec /x/fleet fakeagent admiral'`: "",
	} {
		if got := SessionIDFromCommand(cmd); got != want {
			t.Errorf("%s: got %q want %q", cmd, got, want)
		}
	}
}

func TestSessionRoundTrip(t *testing.T) {
	home := t.TempDir()
	if _, ok := Load(home); ok {
		t.Fatal("missing must not load")
	}
	s := Session{SessionID: "a", Profile: "copilot", PaneID: "%1", Started: time.Now().Round(time.Second), Generation: 2, Previous: "z"}
	if err := Save(home, s); err != nil {
		t.Fatal(err)
	}
	got, ok := Load(home)
	if !ok || got.SessionID != "a" || got.Generation != 2 || got.Previous != "z" || !got.Started.Equal(s.Started) {
		t.Fatalf("%+v", got)
	}
}

func TestNotes(t *testing.T) {
	home := t.TempDir()
	now = func() time.Time { return time.Date(2026, 11, 4, 21, 40, 0, 0, time.Local) }
	t.Cleanup(func() { now = time.Now })
	AppendNote(home, "web-nav", "user wants a ping\nwhen done")
	AppendNote(home, "", "prefers short replies")
	if err := AppendNote(home, "", "  "); err == nil {
		t.Error("empty note must fail")
	}
	got := TailNotes(home, 30)
	want := []string{"2026-11-04T21:40 [web-nav] user wants a ping when done", "2026-11-04T21:40 prefers short replies"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
	if got := TailNotes(home, 1); len(got) != 1 || got[0] != want[1] {
		t.Errorf("tail 1: %q", got)
	}
}

func TestHandoffRotationKeeps20(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 25; i++ {
		if err := WriteHandoff(home, fmt.Sprintf("handoff %d", i)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	es, _ := os.ReadDir(filepath.Join(Dir(home), "handoffs"))
	if len(es) != 20 {
		t.Fatalf("kept %d", len(es))
	}
	b, _ := os.ReadFile(filepath.Join(Dir(home), "handoffs", es[len(es)-1].Name()))
	if strings.TrimSpace(string(b)) != "handoff 23" {
		t.Errorf("newest rotated: %q", b)
	}
	text, mt, ok := ReadHandoff(home)
	if !ok || text != "handoff 24" || time.Since(mt) > time.Minute {
		t.Fatalf("read %q %v %v", text, mt, ok)
	}
	WriteHandoff(home, strings.Repeat("x", 10000))
	if text, _, _ := ReadHandoff(home); len(text) > 4200 || !strings.HasSuffix(text, "[truncated]") {
		t.Errorf("cap: %d", len(text))
	}
}

func TestBandEdges(t *testing.T) {
	t.Setenv("FLEET_ADMIRAL_HANDOFF_PCT", "")
	t.Setenv("FLEET_SUP_HANDOFF_PCT", "")
	for pct, want := range map[int]string{0: theme.Working, 39: theme.Working, 40: theme.Waiting, 69: theme.Waiting, 70: theme.Error, 100: theme.Error} {
		if got := Band(pct); got != want {
			t.Errorf("%d: %s want %s", pct, got, want)
		}
	}
	t.Setenv("FLEET_SUP_HANDOFF_PCT", "60")
	if Band(59) != theme.Waiting || Band(60) != theme.Error || Threshold() != 60 {
		t.Error("threshold from env")
	}
}

func TestThresholdEnv(t *testing.T) {
	t.Setenv("FLEET_ADMIRAL_HANDOFF_PCT", "")
	t.Setenv("FLEET_SUP_HANDOFF_PCT", "55")
	if Threshold() != 55 {
		t.Errorf("deprecated env: %d", Threshold())
	}
	t.Setenv("FLEET_ADMIRAL_HANDOFF_PCT", "80")
	if Threshold() != 80 {
		t.Errorf("new env must win: %d", Threshold())
	}
	t.Setenv("FLEET_SUP_HANDOFF_PCT", "")
	if Threshold() != 80 {
		t.Errorf("new env alone: %d", Threshold())
	}
}

func TestFillCapsTail(t *testing.T) {
	oc, om := tailChunk, maxTail
	tailChunk, maxTail = 1<<10, 4<<10
	t.Cleanup(func() { tailChunk, maxTail = oc, om })
	home := t.TempDir()
	body := usage(1000, 200000) + strings.Repeat(`{"type":"assistant.message","data":{}}`+"\n", 500)
	writeEvents(t, home, "s", body)
	if _, _, ok := Fill(home, "s"); ok {
		t.Error("record beyond the max tail must be unknown")
	}
	writeEvents(t, home, "s", body+usage(5000, 200000))
	if u, _, ok := Fill(home, "s"); !ok || u != 5000 {
		t.Errorf("record in tail: %d %v", u, ok)
	}
}

func TestLockSerialises(t *testing.T) {
	home := t.TempDir()
	unlock, err := Lock(home)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u, _ := Lock(home)
		close(got)
		u()
	}()
	select {
	case <-got:
		t.Fatal("second lock must wait")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("second lock never acquired")
	}
}

func TestHandoffNamesSortByTime(t *testing.T) {
	home := t.TempDir()
	mt := time.Date(2026, 11, 4, 21, 0, 0, 0, time.Local)
	for i := 0; i < 3; i++ { // same mtime every time: names collide
		WriteHandoff(home, fmt.Sprintf("h%d", i))
		os.Chtimes(filepath.Join(Dir(home), "handoff.md"), mt, mt)
	}
	es, _ := os.ReadDir(filepath.Join(Dir(home), "handoffs"))
	var got []string
	for _, e := range es {
		b, _ := os.ReadFile(filepath.Join(Dir(home), "handoffs", e.Name()))
		got = append(got, strings.TrimSpace(string(b)))
	}
	if strings.Join(got, ",") != "h0,h1" {
		t.Errorf("lexical order %v", got)
	}
}

func TestLegacySupervisorDirMigrates(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, "supervisor")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "session.json"), []byte(`{"session_id":"s0","generation":3}`), 0o644)
	s, ok := Load(home)
	if !ok || s.SessionID != "s0" || s.Generation != 3 {
		t.Fatalf("load: %+v %v", s, ok)
	}
	if _, err := os.Stat(filepath.Join(home, "admiral", "session.json")); err != nil {
		t.Fatal("session.json not under admiral/:", err)
	}
	if st, err := os.Lstat(old); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("supervisor/ is not a symlink")
	}
	s.Generation = 4
	if err := Save(home, s); err != nil {
		t.Fatal(err)
	}
	if err := WriteHandoff(home, "next"); err != nil {
		t.Fatal(err)
	}
	// an old binary reading the old path sees the new files
	if b, err := os.ReadFile(filepath.Join(old, "handoff.md")); err != nil || string(b) != "next\n" {
		t.Fatalf("handoff via old path: %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(old, "session.json")); !strings.Contains(string(b), `"generation": 4`) {
		t.Fatalf("session via old path: %s", b)
	}
}

func TestFreshAdmiralDirGetsLegacyLink(t *testing.T) {
	home := t.TempDir()
	if err := Save(home, Session{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(filepath.Join(home, "admiral")); err != nil || !st.IsDir() {
		t.Fatal("admiral/ missing")
	}
	if dst, err := os.Readlink(filepath.Join(home, "supervisor")); err != nil || dst != "admiral" {
		t.Fatalf("legacy link: %q %v", dst, err)
	}
}

// A field a newer fleet adds to session.json survives Load and Save (ADR-62).
func TestSessionKeepsUnknownKeys(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(Dir(home), 0o755)
	os.WriteFile(filepath.Join(Dir(home), "session.json"), []byte(`{"session_id":"s1","generation":2,"k_sess":1}`), 0o644)
	s, ok := Load(home)
	if !ok {
		t.Fatal("not loaded")
	}
	s.Generation++
	if err := Save(home, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(Dir(home), "session.json"))
	if !strings.Contains(string(b), `"k_sess"`) || !strings.Contains(string(b), `"generation": 3`) {
		t.Fatalf("session.json: %s", b)
	}
}

// A pinned note is a standing rule: it is in decisions.log like any note and
// also in pinned.log, which the brief shows whole however many notes follow
// (ADR-62).
func TestPinnedNotes(t *testing.T) {
	home := t.TempDir()
	if err := AppendPinned(home, "", "User rule: never push"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		AppendNote(home, "", fmt.Sprintf("note %d", i))
	}
	pins := Pinned(home)
	if len(pins) != 1 || !strings.HasSuffix(pins[0], " User rule: never push") {
		t.Fatalf("pinned %q", pins)
	}
	if tail := TailNotes(home, 30); strings.Contains(strings.Join(tail, "\n"), "never push") {
		t.Fatal("test setup: the rule is still in the tail")
	}
	b, _ := os.ReadFile(filepath.Join(home, "decisions.log"))
	if !strings.Contains(string(b), "User rule: never push") {
		t.Error("a pinned note is not in decisions.log")
	}
}
