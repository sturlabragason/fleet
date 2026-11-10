package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/sessions"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// adoptFleet builds a Fleet on a fleet-test-* socket with a profile "rs" whose
// sessions live in <tmp>/sessions/{id} and whose resume args are harmless to sh.
func adoptFleet(t *testing.T) (*Fleet, string) {
	t.Helper()
	home := t.TempDir()
	sess := filepath.Join(home, "sessions")
	os.MkdirAll(sess, 0o755)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[rs]
command = "sh"
args = ["-c", "cat"]
resume = ["{id}"]
session_path = "`+sess+`/{id}"
patterns = "fake"

[cl]
command = "sh"
args = ["-c", "cat"]
resume = ["{id}"]
session_path = "`+home+`/projects/*/{id}.jsonl"
patterns = "fake"

[agyish]
command = "sh"
args = ["-c", "cat"]
resume = ["{id}"]
patterns = "fake"

[noresume]
command = "sh"
args = ["-c", "cat"]
`), 0o644)
	// Keep the user's real ~/.config/fleet/config.toml out of the test.
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	for n, p := range f.Profiles { // never read the real ~/.copilot or ~/.claude*
		if n != "rs" && n != "cl" {
			p.SessionPath = ""
			f.Profiles[n] = p
		}
	}
	old := sessions.Procs
	sessions.Procs = func() []sessions.Proc { return nil }
	t.Cleanup(func() { sessions.Procs = old })
	return f, sess
}

func TestAdopt(t *testing.T) {
	f, sess := adoptFleet(t)
	work := filepath.Join(t.TempDir(), "my proj!")
	os.MkdirAll(work, 0o755)
	os.MkdirAll(filepath.Join(sess, "SESSION1"), 0o755)
	os.MkdirAll(filepath.Join(sess, "SESSION2"), 0o755)

	a, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "SESSION1", Dir: work})
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "my-proj" || a.SessionID != "SESSION1" || a.Goal != "" || a.PaneID == "" || a.Dir != work {
		t.Errorf("agent %+v", a)
	}
	if got := f.Store.Load().Agents["my-proj"]; got == nil || got.SessionID != "SESSION1" {
		t.Errorf("board %+v", got)
	}
	if f.paneOf("my-proj") == "" {
		t.Error("no live pane")
	}
	// Same dir again: suffixed name, first board entry untouched.
	b, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "SESSION2", Dir: work})
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "my-proj-2" || f.Store.Load().Agents["my-proj"].SessionID != "SESSION1" {
		t.Errorf("suffix: %s", b.Name)
	}
	// Explicit taken name is an error.
	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "SESSION2", Dir: work, Name: "my-proj", Force: true}); err == nil {
		t.Error("taken --name accepted")
	}
}

func TestAdoptChecks(t *testing.T) {
	f, sess := adoptFleet(t)
	work := t.TempDir()

	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "NOPENOPE", Dir: work}); err == nil || !strings.Contains(err.Error(), "no rs session NOPENOPE") {
		t.Errorf("missing session: %v", err)
	}
	if _, err := f.Adopt(AdoptOpts{Profile: "noresume", SessionID: "XXXXXXXX", Dir: work}); err == nil || !strings.Contains(err.Error(), "can't resume") {
		t.Errorf("no resume: %v", err)
	}
	// Lock held by a live pid (our own) refuses; --force bypasses.
	sd := filepath.Join(sess, "LOCKED01")
	os.MkdirAll(sd, 0o755)
	os.WriteFile(filepath.Join(sd, fmt.Sprintf("inuse.%d.lock", os.Getpid())), nil, 0o644)
	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "LOCKED01", Dir: work}); err == nil || !strings.Contains(err.Error(), "in use by pid") {
		t.Errorf("lock: %v", err)
	}
	// Process scan hit refuses.
	os.MkdirAll(filepath.Join(sess, "PROCHIT1"), 0o755)
	sessions.Procs = func() []sessions.Proc {
		return []sessions.Proc{{PID: 4242, Comm: "copilot", Argv: []string{"copilot", "--resume", "PROCHIT1"}}}
	}
	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "PROCHIT1", Dir: work}); err == nil || !strings.Contains(err.Error(), "pid 4242") {
		t.Errorf("proc: %v", err)
	}
	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "LOCKED01", Dir: work, Name: "forced", Force: true}); err != nil {
		t.Errorf("force: %v", err)
	}
	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "NOPENOPE", Dir: work, Name: "forced2", Force: true}); err != nil {
		t.Errorf("force missing: %v", err)
	}
	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "LOCKED01", Dir: filepath.Join(work, "missing"), Force: true}); err == nil {
		t.Error("missing dir accepted")
	}
}

func TestAdoptWorkspaceCwd(t *testing.T) {
	f, sess := adoptFleet(t)
	work := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(work, 0o755)
	sd := filepath.Join(sess, "WORKSP01")
	os.MkdirAll(sd, 0o755)
	os.WriteFile(filepath.Join(sd, "workspace.yaml"), []byte("id: WORKSP01\ncwd: "+work+"\nsummary: x\n"), 0o644)
	a, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "WORKSP01"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Dir != work || a.Name != "repo" {
		t.Errorf("dir %s name %s", a.Dir, a.Name)
	}
}

func TestAdoptIDAndLongName(t *testing.T) {
	f, sess := adoptFleet(t)
	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "short1", Dir: t.TempDir(), Force: true}); err == nil || !strings.Contains(err.Error(), "bad session id") {
		t.Errorf("short id: %v", err)
	}
	long := filepath.Join(t.TempDir(), strings.Repeat("abcdefghij", 4))
	os.MkdirAll(long, 0o755)
	os.MkdirAll(filepath.Join(sess, "LONGNAME1"), 0o755)
	os.MkdirAll(filepath.Join(sess, "LONGNAME2"), 0o755)
	a, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "LONGNAME1", Dir: long})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "LONGNAME2", Dir: long})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Name) != 32 || len(b.Name) > 32 || !strings.HasSuffix(b.Name, "-2") || a.Name == b.Name {
		t.Errorf("names %q %q", a.Name, b.Name)
	}
}

// copilotSession writes a fake Copilot session folder with a name, cwd and a prompt.
func copilotSession(t *testing.T, sess, id, name, cwd string) {
	t.Helper()
	d := filepath.Join(sess, id)
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "workspace.yaml"), []byte("id: "+id+"\ncwd: "+cwd+"\nname: "+name+"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "events.jsonl"), []byte(`{"type":"session.start","data":{"selectedModel":"gpt-5"}}`+"\n"+`{"type":"user.message","data":{"content":"hi"}}`+"\n"), 0o644)
}

func TestAdoptByWords(t *testing.T) {
	f, sess := adoptFleet(t)
	work := filepath.Join(t.TempDir(), "bs")
	os.MkdirAll(work, 0o755)
	copilotSession(t, sess, "aaaa1111-0000-0000-0000-000000000001", "Portal nav sidebar", work)
	copilotSession(t, sess, "aaaa2222-0000-0000-0000-000000000002", "Portal catalog fix", work)
	copilotSession(t, sess, "bbbb3333-0000-0000-0000-000000000003", "Terraform provider", work)

	a, err := f.AdoptArgs(AdoptOpts{}, []string{"portal", "NAV"})
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionID != "aaaa1111-0000-0000-0000-000000000001" || a.Profile != "rs" || a.Dir != work {
		t.Errorf("adopted %+v", a)
	}
	// Now on the board: the same words find nothing.
	if _, err := f.AdoptArgs(AdoptOpts{}, []string{"portal", "nav"}); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("on board: %v", err)
	}
	// Several matches list candidates (profile given as first positional).
	copilotSession(t, sess, "aaaa4444-0000-0000-0000-000000000004", "Portal theme", work)
	_, err = f.AdoptArgs(AdoptOpts{}, []string{"rs", "portal"})
	if err == nil || !strings.Contains(err.Error(), "2 sessions match") || !strings.Contains(err.Error(), "  aaaa2222  ") || !strings.Contains(err.Error(), "be more specific") {
		t.Errorf("several: %v", err)
	}
	// A unique 8-char prefix resolves to the full ID; an ambiguous one errors.
	b, err := f.AdoptArgs(AdoptOpts{Name: "tf"}, []string{"bbbb3333"})
	if err != nil {
		t.Fatal(err)
	}
	if b.SessionID != "bbbb3333-0000-0000-0000-000000000003" || b.Profile != "rs" {
		t.Errorf("prefix %+v", b)
	}
	if _, err := f.AdoptArgs(AdoptOpts{}, []string{"aaaa2222-0000", t.TempDir()}); err != nil {
		t.Errorf("prefix + dir: %v", err)
	}
	if _, err := f.AdoptArgs(AdoptOpts{}, []string{"zzzz"}); err == nil || !strings.Contains(err.Error(), "no session matches \"zzzz\"; try fleet sessions") {
		t.Errorf("no match: %v", err)
	}
	copilotSession(t, sess, "cccc5555-0000-0000-0000-000000000005", "x", work)
	copilotSession(t, sess, "cccc5555-0000-0000-0000-000000000006", "y", work)
	if _, err := f.AdoptArgs(AdoptOpts{}, []string{"cccc5555"}); err == nil || !strings.Contains(err.Error(), "2 sessions start with") {
		t.Errorf("ambiguous prefix: %v", err)
	}
}

// A Claude transcript has no workspace.yaml: the dir comes from the resolved session.
func TestAdoptClaudeByWords(t *testing.T) {
	f, _ := adoptFleet(t)
	work := filepath.Join(t.TempDir(), "ledger")
	os.MkdirAll(work, 0o755)
	pd := filepath.Join(f.Home, "projects", "-x-ledger")
	os.MkdirAll(pd, 0o755)
	os.WriteFile(filepath.Join(pd, "dddd4444-0000-0000-0000-000000000004.jsonl"),
		[]byte(`{"type":"user","cwd":"`+work+`","message":{"role":"user","content":"invoice roles please"}}`+"\n"), 0o644)
	a, err := f.AdoptArgs(AdoptOpts{}, []string{"invoice", "roles"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Dir != work || a.Profile != "cl" || a.SessionID != "dddd4444-0000-0000-0000-000000000004" {
		t.Errorf("adopted %+v", a)
	}
}

// A profile without a session store takes only an ID-shaped first arg.
func TestAdoptStorelessWords(t *testing.T) {
	f, _ := adoptFleet(t)
	want := "profile agyish has no session store to search; pass the session ID"
	for _, args := range [][]string{{"agyish", "fix", "nav"}, {"agyish", "fix"}} {
		if _, err := f.AdoptArgs(AdoptOpts{}, args); err == nil || err.Error() != want {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := f.AdoptArgs(AdoptOpts{}, []string{"agyish", "eeee5555-0000", t.TempDir()}); err != nil {
		t.Errorf("store-less id: %v", err)
	}
}

// Adopt takes the driver like spawn: a profile without an ACP server refuses
// --driver acp before anything starts, and acp needs no resume args.
func TestAdoptDriver(t *testing.T) {
	f, sess := adoptFleet(t)
	os.MkdirAll(filepath.Join(sess, "SESSION1"), 0o755)
	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "SESSION1", Driver: "acp"}); err == nil || !strings.Contains(err.Error(), "no ACP server") {
		t.Errorf("adopt --driver acp on rs: %v", err)
	}
	if _, err := f.Adopt(AdoptOpts{Profile: "noresume", SessionID: "SESSION1", Driver: "tmux"}); err == nil || !strings.Contains(err.Error(), "can't resume") {
		t.Errorf("adopt noresume on tmux: %v", err)
	}
	if len(f.Store.Load().Agents) != 0 {
		t.Error("a refused adopt left a board entry")
	}
}
