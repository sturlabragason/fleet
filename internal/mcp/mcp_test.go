package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/wall"
)

func TestRoundTrip(t *testing.T) {
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[yn]\ncommand = \"sh\"\nargs = [\"-c\", \"printf 'Approve? [y/n] '; cat\"]\npatterns = \"fake\"\n"), 0o644)
	// Keep the user's real ~/.config/fleet/config.toml out of the test.
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("a1", "yn", home, ""); err != nil {
		t.Fatal(err)
	}
	// Wait for the prompt rather than a fixed sleep (flaky under a loaded suite).
	for i := 0; i < 50; i++ {
		if p, _ := f.Peek("a1", 0); strings.Contains(p, "[y/n]") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go New(f, wall.New(tc, fleet.Session)).Serve(inR, outW)
	dec := bufio.NewScanner(outR)
	call := func(id int, method string, params any) map[string]any {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		inW.Write(append(b, '\n'))
		if !dec.Scan() {
			t.Fatal("no response")
		}
		var r map[string]any
		json.Unmarshal(dec.Bytes(), &r)
		return r
	}
	init := call(1, "initialize", map[string]any{"protocolVersion": ProtocolVersion})
	if init["result"].(map[string]any)["protocolVersion"] != ProtocolVersion {
		t.Fatalf("init %v", init)
	}
	inW.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	list := call(2, "tools/list", nil)
	names := ""
	for _, tl := range list["result"].(map[string]any)["tools"].([]any) {
		names += tl.(map[string]any)["name"].(string) + " "
	}
	for _, want := range []string{"fleet_status", "fleet_peek", "fleet_send", "fleet_answer", "fleet_spawn", "fleet_sessions", "fleet_adopt", "fleet_profiles", "fleet_close", "fleet_wall", "fleet_note", "fleet_brief", "fleet_handoff", "fleet_team", "fleet_info"} {
		if !strings.Contains(names, want) {
			t.Errorf("missing tool %s", want)
		}
	}
	text := func(r map[string]any) (string, bool) {
		res := r["result"].(map[string]any)
		return res["content"].([]any)[0].(map[string]any)["text"].(string), res["isError"].(bool)
	}
	// The prompt can take a while to appear on slow machines (CI).
	var st string
	var isErr bool
	for i := 0; i < 50; i++ {
		st, isErr = text(call(100+i, "tools/call", map[string]any{"name": "fleet_status", "arguments": map[string]any{}}))
		if strings.Contains(st, "a1 |  | yn | waiting") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if isErr || !strings.Contains(st, "a1 |  | yn | waiting") {
		t.Errorf("status: %q", st)
	}
	if _, isErr := text(call(4, "tools/call", map[string]any{"name": "fleet_answer", "arguments": map[string]any{"name": "a1", "answer": "y"}})); !isErr {
		t.Error("unconfirmed answer must be refused")
	}
	if _, isErr := text(call(5, "tools/call", map[string]any{"name": "fleet_answer", "arguments": map[string]any{"name": "a1", "answer": "y", "confirmed": true}})); isErr {
		t.Error("confirmed answer failed")
	}
	if _, isErr := text(call(6, "tools/call", map[string]any{"name": "fleet_close", "arguments": map[string]any{"name": "a1"}})); !isErr {
		t.Error("unconfirmed close must be refused")
	}
	if msg, isErr := text(call(8, "tools/call", map[string]any{"name": "fleet_adopt", "arguments": map[string]any{"profile": "fake", "session_id": "abcdefgh"}})); !isErr || !strings.Contains(msg, "can't resume") {
		t.Errorf("adopt: %v %q", isErr, msg)
	}
	for _, args := range []map[string]any{{}, {"session_id": "abcdefgh", "query": "x"}} {
		if msg, isErr := text(call(9, "tools/call", map[string]any{"name": "fleet_adopt", "arguments": args})); !isErr || !strings.Contains(msg, "exactly one of session_id or query") {
			t.Errorf("adopt %v: %v %q", args, isErr, msg)
		}
	}
	// fleet_sessions on a temp store only (never the real ~/.copilot or ~/.claude*).
	sess := filepath.Join(home, "sessions")
	for n, p := range f.Profiles {
		p.SessionPath = ""
		f.Profiles[n] = p
	}
	f.Profiles["rs"] = tools.Profile{Name: "rs", Command: "sh", Resume: []string{"{id}"}, SessionPath: sess + "/{id}"}
	sd := filepath.Join(sess, "mcpsess-0001")
	os.MkdirAll(sd, 0o755)
	os.WriteFile(filepath.Join(sd, "workspace.yaml"), []byte("cwd: "+home+"\nname: Portal nav\n"), 0o644)
	os.WriteFile(filepath.Join(sd, "events.jsonl"), []byte(`{"type":"user.message","data":{"content":"go"}}`+"\n"), 0o644)
	if msg, isErr := text(call(10, "tools/call", map[string]any{"name": "fleet_sessions", "arguments": map[string]any{"query": "nav"}})); isErr || !strings.Contains(msg, "mcpsess-0001 | ") || !strings.Contains(msg, "| rs | ") || !strings.Contains(msg, "Portal nav") {
		t.Errorf("sessions: %v %q", isErr, msg)
	}
	if msg, isErr := text(call(11, "tools/call", map[string]any{"name": "fleet_adopt", "arguments": map[string]any{"query": "nothing-here"}})); !isErr || !strings.Contains(msg, "no session matches") {
		t.Errorf("adopt query: %v %q", isErr, msg)
	}
	// Claude-style store: adopt by padded query and by padded id lands in the transcript's cwd.
	cwd := t.TempDir()
	pd := filepath.Join(home, "projects", "-p")
	os.MkdirAll(pd, 0o755)
	f.Profiles["cl"] = tools.Profile{Name: "cl", Command: "sh", Args: []string{"-c", "cat"}, Resume: []string{"{id}"}, SessionPath: home + "/projects/*/{id}.jsonl"}
	for i, id := range []string{"ffff6666-0000-0000-0000-000000000001", "ffff6666-0000-0000-0000-000000000002"} {
		os.WriteFile(filepath.Join(pd, id+".jsonl"), []byte(`{"type":"user","cwd":"`+cwd+`","message":{"content":"grafana `+[]string{"alpha", "beta"}[i]+`"}}`+"\n"), 0o644)
	}
	for i, args := range []map[string]any{{"query": "  grafana   alpha ", "session_id": "  "}, {"session_id": " ffff6666-0000-0000-0000-000000000002 ", "query": ""}} {
		args["name"] = "cl" + strconv.Itoa(i)
		if msg, isErr := text(call(12+i, "tools/call", map[string]any{"name": "fleet_adopt", "arguments": args})); isErr || !strings.Contains(msg, "in "+cwd) {
			t.Errorf("adopt claude %v: %v %q", args, isErr, msg)
		}
	}
	if r := call(7, "nope", nil); r["error"] == nil {
		t.Error("want method not found")
	}
	inW.Close()
}

// Quick wins (DESIGN §6.8): multi-target send, keys, numbered answers,
// cleaned peek, last_message, fleet_info and the close note.
func TestSupervisorErgonomics(t *testing.T) {
	home := t.TempDir()
	// worktree = false: this test watches the git state of the spawn dir
	// itself, so a1 works in the repo rather than a worktree of its own.
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nworktree = false\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[yn]
command = "sh"
args = ["-c", "printf 'Approve? [y/n] '; cat"]
patterns = "fake"
subagent_log = "`+home+`/{id}.jsonl"
[menu]
command = "sh"
args = ["-c", "stty -echo; printf '│ Do you want to allow this?                 │\n│ ❯ 1. Yes                                   │\n│   2. Yes, and add these directories to the allowed list │\n│   3. No, and tell Copilot what to do differently │\n╰────────────────────────────────────────────╯\n'; cat -v"]
patterns = "copilot"
`), 0o644)
	repo := filepath.Join(home, "repo")
	os.MkdirAll(repo, 0o755)
	for _, c := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "a"}, {"checkout", "-q", "-b", "feat"}, {"commit", "-q", "--allow-empty", "-m", "b"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, c...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", c, err, out)
		}
	}
	os.WriteFile(filepath.Join(repo, "x.txt"), []byte("x"), 0o644)
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	for n, p := range map[string]string{"a1": "yn", "a2": "yn", "m": "menu"} {
		dir := home
		if n == "a1" {
			dir = repo
		}
		if _, err := f.Spawn(n, p, dir, ""); err != nil {
			t.Fatal(err)
		}
	}
	s := New(f, wall.New(tc, fleet.Session))
	s.Fill = func() (int, int, bool) { return 0, 0, false }
	time.Sleep(400 * time.Millisecond)
	call := func(tool string, a map[string]any) (string, bool) { return callText(t, s, tool, a) }

	st, _ := call("fleet_status", nil)
	if !strings.Contains(st, "Do you want to allow this? [1 Yes | 2 Yes, and add these directorie… | 3 No, and tell Copilot what to …]") {
		t.Errorf("status menu: %q", st)
	}
	// a1 is on its [y/n] prompt: plain text would answer it, so it needs confirmed.
	if out, isErr := call("fleet_send", map[string]any{"names": []string{"a1", "ghost"}, "text": "hi all"}); isErr || !strings.Contains(out, "a1: skipped (agent is waiting on a prompt") || !strings.Contains(out, "\nghost: skipped (agent \"ghost\" not running)") {
		t.Errorf("names unconfirmed: %v %q", isErr, out)
	}
	if out, isErr := call("fleet_send", map[string]any{"names": []string{"a1", "ghost"}, "text": "hi all", "confirmed": true}); isErr || !strings.Contains(out, "a1: sent\nghost: skipped (agent \"ghost\" not running)") {
		t.Errorf("names: %v %q", isErr, out)
	}
	f.Teams.Add("t", "a1", "a2")
	if out, isErr := call("fleet_send", map[string]any{"team": "t", "keys": []string{"y"}}); isErr || !strings.Contains(out, "a1: sent\na2: skipped (agent is waiting on a prompt") { // a1 left its prompt with "hi all"
		t.Errorf("team keys unconfirmed: %v %q", isErr, out)
	}
	if out, isErr := call("fleet_team", map[string]any{"action": "send", "team": "t", "text": "x"}); isErr || !strings.HasPrefix(out, "t: sent: a1; skipped: a2 (agent is waiting on a prompt") {
		t.Errorf("fleet_team send unconfirmed: %v %q", isErr, out)
	}
	if out, isErr := call("fleet_send", map[string]any{"name": "a2", "keys": []string{"C-c"}, "confirmed": true}); !isErr || !strings.Contains(out, `"C-c" not allowed`) {
		t.Errorf("C-c: %v %q", isErr, out)
	}
	if out, isErr := call("fleet_send", map[string]any{"name": "a2", "keys": []string{"Enter"}}); !isErr || !strings.Contains(out, "waiting on a prompt") {
		t.Errorf("keys to waiting: %v %q", isErr, out)
	}
	if out, isErr := call("fleet_send", map[string]any{"name": "a2", "text": "typed", "keys": []string{"Enter"}, "confirmed": true}); isErr {
		t.Errorf("confirmed keys: %q", out)
	}
	if _, isErr := call("fleet_send", map[string]any{"name": "a2", "team": "t", "text": "x"}); !isErr {
		t.Error("name and team both accepted")
	}
	for _, a := range []map[string]any{{"name": "m", "confirmed": true}, {"name": "m", "answer": "y", "option": 2, "confirmed": true}} {
		if out, isErr := call("fleet_answer", a); !isErr || !strings.Contains(out, "exactly one of answer") {
			t.Errorf("answer %v: %q", a, out)
		}
	}
	if out, isErr := call("fleet_answer", map[string]any{"name": "m", "option": 2, "expect": "run this command", "confirmed": true}); !isErr || out != "prompt changed: now 'Do you want to allow this?'" {
		t.Errorf("expect mismatch: %v %q", isErr, out)
	}
	if out, isErr := call("fleet_answer", map[string]any{"name": "m", "option": 2, "expect": "Allow This", "confirmed": true}); isErr || out != `answered "2. Yes, and add these directories to the allowed list" to m (prompt: "Do you want to allow this?")` {
		t.Errorf("option: %v %q", isErr, out)
	}
	var pane string
	for i := 0; i < 30 && !strings.Contains(pane, "^["); i++ {
		time.Sleep(50 * time.Millisecond)
		pane, _ = f.Peek("m", 0)
	}
	if !strings.HasSuffix(strings.ReplaceAll(pane, "^[O", "^[["), "\n^[[B") {
		t.Errorf("want Down then Enter: %q", pane)
	}
	if out, isErr := call("fleet_peek", map[string]any{"name": "m"}); isErr || strings.Contains(out, "│") || strings.Contains(out, "╰") || !strings.Contains(out, "\n❯ 1. Yes\n") {
		t.Errorf("clean peek: %q", out)
	}
	if out, _ := call("fleet_peek", map[string]any{"name": "m", "raw": true}); !strings.Contains(out, "╰──") {
		t.Errorf("raw peek: %q", out)
	}
	if out, isErr := call("fleet_peek", map[string]any{"name": "m", "last_message": true}); !isErr || !strings.Contains(out, "last_message not available for profile menu") {
		t.Errorf("last_message menu: %q", out)
	}
	f.Store.Update(func(b *board.Board) error { b.Agents["a2"].SessionID = "s1"; return nil })
	os.WriteFile(filepath.Join(home, "s1.jsonl"), []byte(`{"type":"assistant.message","data":{"content":"all done"}}`+"\n"+`{"type":"assistant.message","data":{"content":"sub","agentId":"x"}}`+"\n"), 0o644)
	if out, isErr := call("fleet_peek", map[string]any{"name": "a2", "last_message": true}); isErr || !strings.HasPrefix(out, "all done") {
		t.Errorf("last_message: %q", out)
	}
	out, isErr := call("fleet_info", map[string]any{"name": "a1"})
	for _, want := range []string{"profile: yn", "team: t", "status: ", "git branch: feat", "git dirty: 1 files", "git merged into main: no", "git ahead of main: 1", "dir: " + repo} {
		if isErr || !strings.Contains(out, want) {
			t.Errorf("info missing %q: %q", want, out)
		}
	}
	if out, _ := call("fleet_info", map[string]any{"name": "a2"}); !strings.Contains(out, "git: not a git work tree") || !strings.Contains(out, "session: s1") {
		t.Errorf("info a2: %q", out)
	}
	if out, isErr := call("fleet_close", map[string]any{"name": "a1"}); !isErr || !strings.Contains(out, "Note: a1 has uncommitted changes in "+repo) {
		t.Errorf("close note: %q", out)
	}
	if out, isErr := call("fleet_close", map[string]any{"name": "a1", "confirmed": true}); isErr || !strings.HasPrefix(out, "closed a1. Note: a1 has uncommitted") {
		t.Errorf("close: %q", out)
	}
}
