package fleet

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

func TestDriverResolution(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\ndriver = \"acp\"\n[profile.agy]\ndriver = \"tmux\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f, err := New(&tmux.Client{Socket: "fleet-test-unused"}, home, "/bin/fleet")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ flag, profile, want, err string }{
		{"", "copilot", "acp", ""},      // [defaults]
		{"", "agy", "tmux", ""},         // [profile.agy] beats [defaults]
		{"acp", "agy", "acp", ""},       // the flag beats both
		{"tmux", "copilot", "tmux", ""}, // and back
		{"", "claude", "tmux", ""},      // ADR-76: [defaults] never moves claude to acp
		{"acp", "claude", "acp", ""},    // but --driver acp does
		{"acp", "vibe", "", "no ACP server"},
		{"tmux", "claude", "tmux", ""},
		{"ssh", "copilot", "", "unknown driver"},
	}
	for _, c := range cases {
		got, err := f.driverFor(c.flag, c.profile)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q %s: err %v want %q", c.flag, c.profile, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q %s: %q, %v want %q", c.flag, c.profile, got, err, c.want)
		}
	}
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
	f, _ = New(&tmux.Client{Socket: "fleet-test-unused"}, home, "/bin/fleet")
	// v1.0.0 (ADR-74): with no config, copilot and agy default to acp and
	// every other profile stays on tmux; (i): so does every profile that
	// runs copilot, claude-copilot first.
	for profile, want := range map[string]string{"copilot": "acp", "agy": "acp", "claude-copilot": "acp", "claude": "tmux", "vibe": "tmux", "fake": "tmux"} {
		if got, err := f.driverFor("", profile); err != nil || got != want {
			t.Errorf("no config, %s: %q, %v want %q", profile, got, err, want)
		}
	}
	if _, err := f.SpawnWith(SpawnOpts{Driver: "acp"}, "c1", "vibe", home, ""); err == nil || !strings.Contains(err.Error(), "no ACP server") {
		t.Errorf("spawn vibe --driver acp: %v", err)
	}
	if len(f.Store.Load().Agents) != 0 {
		t.Error("a refused spawn left a board entry")
	}
}

// The migration note's two ways back to tmux, and the per-spawn override,
// beat the v1.0.0 default (ADR-74 (f)).
func TestDriverDefaultOverrides(t *testing.T) {
	home := t.TempDir()
	for _, c := range []struct{ conf, flag, profile, want string }{
		{"[defaults]\ndriver = \"tmux\"\n", "", "copilot", "tmux"},
		{"[defaults]\ndriver = \"tmux\"\n", "", "agy", "tmux"},
		{"[defaults]\ndriver = \"tmux\"\n", "acp", "agy", "acp"},
		{"[profile.copilot]\ndriver = \"tmux\"\n", "", "copilot", "tmux"},
		{"[profile.copilot]\ndriver = \"tmux\"\n", "", "agy", "acp"},
		{"[defaults]\ndriver = \"bogus\"\n", "", "copilot", "acp"},
		{"", "tmux", "copilot", "tmux"},
		{"", "tmux", "agy", "tmux"},
		// claude-copilot (ADR-74 (i)): acp by default, and every way back wins.
		{"", "", "claude-copilot", "acp"},
		{"[defaults]\ndriver = \"tmux\"\n", "", "claude-copilot", "tmux"},
		{"[profile.claude-copilot]\ndriver = \"tmux\"\n", "", "claude-copilot", "tmux"},
		{"[profile.copilot]\ndriver = \"tmux\"\n", "", "claude-copilot", "acp"},
		{"[profile.claude-copilot]\ndriver = \"tmux\"\n", "acp", "claude-copilot", "acp"},
		{"", "tmux", "claude-copilot", "tmux"},
	} {
		cfg := filepath.Join(home, "config.toml")
		os.WriteFile(cfg, []byte(c.conf), 0o644)
		t.Setenv("FLEET_CONFIG", cfg)
		f, err := New(&tmux.Client{Socket: "fleet-test-unused"}, home, "/bin/fleet")
		if err != nil {
			t.Fatal(err)
		}
		if got, err := f.driverFor(c.flag, c.profile); err != nil || got != c.want {
			t.Errorf("%q flag %q %s: %q, %v want %q", c.conf, c.flag, c.profile, got, err, c.want)
		}
	}
}

func TestACPHostArgvGolden(t *testing.T) {
	got := ACPHostArgv("/bin/fleet", "a1", "/w", ACPHostOpts{Session: "S1", PromptFile: "/h/acp/a1.prompt", CopilotEvents: true}, []string{"copilot", "--acp"})
	want := []string{"/bin/fleet", "acp-host", "a1", "--dir", "/w", "--session", "S1", "--prompt-file", "/h/acp/a1.prompt",
		"--copilot-events", "--", "copilot", "--acp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q\nwant %q", got, want)
	}
	got = ACPHostArgv("/bin/fleet", "a1", "/w", ACPHostOpts{Mode: "plan", MCP: "{}", UnsetEnv: []string{"A", "B"}}, []string{"npx", "x"})
	want = []string{"/bin/fleet", "acp-host", "a1", "--dir", "/w", "--mode", "plan", "--mcp", "{}", "--unset-env", "A,B", "--", "npx", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q\nwant %q", got, want)
	}
	got = ACPHostArgv("/bin/fleet", "a1", "/w", ACPHostOpts{}, []string{"agy", "acp"})
	want = []string{"/bin/fleet", "acp-host", "a1", "--dir", "/w", "--", "agy", "acp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q\nwant %q", got, want)
	}
}

// stubHost answers control requests on an agent's socket the way
// `fleet acp-host` does, and records them.
type stubHost struct {
	mu       sync.Mutex
	reqs     []acphost.Request
	question *acphost.Question
	// exitAfter > 0: a stop closes the socket that long after answering,
	// as the real host does once session/close and its child are done.
	exitAfter time.Duration
}

func (s *stubHost) serve(t *testing.T, home, name string) {
	t.Helper()
	os.MkdirAll(acphost.Dir(home), 0o700)
	l, err := net.Listen("unix", acphost.SocketPath(home, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadBytes('\n')
			var req acphost.Request
			json.Unmarshal(line, &req)
			s.mu.Lock()
			s.reqs = append(s.reqs, req)
			resp := acphost.Response{OK: true, Status: "idle", Question: s.question}
			if req.Op == acphost.OpAnswer {
				s.question = nil
			}
			exit := s.exitAfter
			s.mu.Unlock()
			if req.Op == acphost.OpStop && exit > 0 {
				time.AfterFunc(exit, func() { l.Close() })
			}
			b, _ := json.Marshal(resp)
			c.Write(append(b, '\n'))
			c.Close()
		}
	}()
}

func (s *stubHost) ops() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.reqs {
		o := r.Op
		switch {
		case r.Text != "":
			o += ":" + r.Text
		case r.OptionID != "":
			o += ":" + r.OptionID
		}
		out = append(out, o)
	}
	return out
}

// untilStop drops what follows the first stop: the state polls of the wait
// for the host to exit, as many as timing gives.
func untilStop(ops []string) []string {
	for i, o := range ops {
		if o == "stop" {
			return ops[:i+1]
		}
	}
	return ops
}

func (s *stubHost) reset() {
	s.mu.Lock()
	s.reqs = nil
	s.mu.Unlock()
}

// tmuxLog puts a tmux on PATH that logs every call and runs the real one.
func tmuxLog(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "tmux.log")
	os.WriteFile(filepath.Join(dir, "tmux"), []byte(fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %s\nexec %s \"$@\"\n", log, real)), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return log
}

func captures(t *testing.T, log, pane string) int {
	t.Helper()
	b, _ := os.ReadFile(log)
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "capture-pane") && strings.HasSuffix(strings.TrimSpace(l), "-t "+pane) {
			n++
		}
	}
	return n
}

func writeTranscript(t *testing.T, home, name string, es ...acphost.Entry) {
	t.Helper()
	fh, err := os.OpenFile(acphost.TranscriptPath(home, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	for _, e := range es {
		b, _ := json.Marshal(e)
		fh.Write(append(b, '\n'))
	}
}

func startCommand(t *testing.T, tc *tmux.Client, pane string) string {
	t.Helper()
	out, err := tc.Run("display-message", "-p", "-t", pane, "#{pane_start_command}")
	if err != nil {
		t.Fatal(err)
	}
	// tmux shows the command double-quoted when it holds quotes.
	if len(out) > 1 && out[0] == '"' && out[len(out)-1] == '"' {
		out = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\$`, `$`).Replace(out[1 : len(out)-1])
	}
	return out
}

// hostArgs is the argv each fake host start received, oldest first.
func hostArgs(t *testing.T, home string) [][]string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(home, "host.args"))
		var out [][]string
		for _, run := range strings.Split(string(b), "---\n") {
			if run != "" {
				out = append(out, strings.Split(strings.TrimSuffix(run, "\n"), "\n"))
			}
		}
		if len(out) > 0 || time.Now().After(deadline) {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Claude over the adapter (ADR-76), with a stub host that records its argv
// and env: the adapter argv, the session mode, peer MCP over the session,
// the dropped secrets, the installed claude in CLAUDE_CODE_EXECUTABLE; then
// a wake on the same session; and a claude agent on tmux beside it that
// launches exactly as before.
func TestClaudeACPSpawnAndWake(t *testing.T) {
	tmuxLog(t)
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\nmode = \"auto\"\ndriver = \"acp\"\nauto_trust = false\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	t.Setenv("HOME", t.TempDir()) // no ~/.local/bin/claude ahead of the test's
	bin := t.TempDir()
	claude := filepath.Join(bin, "claude")
	os.WriteFile(claude, []byte("#!/bin/sh\nexec sleep 600\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	host := filepath.Join(home, "fakehost")
	os.WriteFile(host, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+home+"/host.args\necho --- >> "+home+"/host.args\n"+
		"echo \"exe=$CLAUDE_CODE_EXECUTABLE model=$ANTHROPIC_MODEL sub=$CLAUDE_CODE_SUBAGENT_MODEL\" >> "+home+"/host.env\nexec sleep 600\n"), 0o755)
	f, err := New(tc, home, host)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wt := false
	// [defaults] driver = "acp" leaves claude on tmux (opt-in), as before.
	tm, err := f.SpawnWith(SpawnOpts{Worktree: &wt}, "t1", "claude", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if tm.ACP() {
		t.Fatal("claude went to acp without opting in")
	}
	if got := startCommand(t, tc, tm.PaneID); !strings.Contains(got, "claude --mcp-config") || !strings.Contains(got, "--permission-mode bypassPermissions") || strings.Contains(got, "acp-host") {
		t.Errorf("claude tmux command %q", got)
	}

	a, err := f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, "a1", "claude", dir, "do it")
	if err != nil {
		t.Fatal(err)
	}
	if !a.ACP() {
		t.Fatalf("driver %q", a.Driver)
	}
	_, peer := tools.PeerMCP(host, "a1")
	wantArgv := []string{"acp-host", "a1", "--dir", dir, "--prompt-file", ACPPromptPath(home, "a1"),
		"--mode", "bypassPermissions", "--mcp", peer, "--unset-env", "ANTHROPIC_API_KEY,ANTHROPIC_AUTH_TOKEN,CLAUDE_CODE_OAUTH_TOKEN",
		"--", "npx", "-y", "@agentclientprotocol/claude-agent-acp@" + tools.ClaudeACPVersion}
	got := hostArgs(t, home)
	if len(got) != 1 || !reflect.DeepEqual(got[0], wantArgv) {
		t.Errorf("host argv %q\nwant %q", got, wantArgv)
	}
	envOf := func(n int) string {
		deadline := time.Now().Add(5 * time.Second)
		for {
			b, _ := os.ReadFile(filepath.Join(home, "host.env"))
			if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); len(lines) >= n && lines[0] != "" {
				return lines[n-1]
			}
			if time.Now().After(deadline) {
				t.Fatalf("host env: %q", b)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if e, want := envOf(1), "exe="+claude+" model=opus sub=sonnet"; e != want {
		t.Errorf("host env %q want %q", e, want)
	}

	// Wake resumes the same session with the same mode, MCP and env.
	os.MkdirAll(acphost.Dir(home), 0o700)
	os.WriteFile(acphost.TranscriptPath(home, "a1"), []byte(`{"kind":"session","session_id":"S9","text":"new"}`+"\n"), 0o600)
	f.Store.Update(func(b *board.Board) error {
		b.Agents["a1"].Status = "idle"
		b.Agents["a1"].SessionID = "S9"
		return nil
	})
	(&stubHost{exitAfter: 10 * time.Millisecond}).serve(t, home, "a1")
	if err := f.Park("a1", ""); err != nil {
		t.Fatal(err)
	}
	w, err := f.Wake("a1", true)
	if err != nil {
		t.Fatal(err)
	}
	// The stub never sent the spawn's prompt, so the wake carries its file.
	wantWake := append([]string{host, "acp-host", "a1", "--dir", dir, "--session", "S9"}, wantArgv[4:]...)
	if got := startCommand(t, tc, w.PaneID); got != tools.ShellCommand(wantWake) {
		t.Errorf("wake command %s\nwant %s", got, tools.ShellCommand(wantWake))
	}
	if e := envOf(2); !strings.HasPrefix(e, "exe="+claude+" ") {
		t.Errorf("wake env %q", e)
	}
}

// Without the installed claude on PATH a claude acp spawn is refused: the
// adapter must never fall back to a claude of its own (ADR-76).
func TestClaudeACPNeedsInstalledClaude(t *testing.T) {
	home := t.TempDir()
	noPlaybook(t, home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	f, err := New(&tmux.Client{Socket: "fleet-test-unused"}, home, "/bin/fleet")
	if err != nil {
		t.Fatal(err)
	}
	wt := false
	if _, err := f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, "c1", "claude", t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "not on PATH") {
		t.Errorf("spawn without claude on PATH: %v", err)
	}
	if len(f.Store.Load().Agents) != 0 {
		t.Error("a refused spawn left a board entry")
	}
	// A parked claude ACP agent whose claude went away stays parked.
	f.Store.Update(func(b *board.Board) error {
		b.Agents["c2"] = &board.Agent{Name: "c2", Profile: "claude", Dir: t.TempDir(), Status: string(status.Parked), SessionID: "s1", Driver: board.DriverACP}
		return nil
	})
	if _, err := f.Wake("c2", true); err == nil || !strings.Contains(err.Error(), "not on PATH") {
		t.Errorf("wake without claude on PATH: %v", err)
	}
	if got := f.Store.Load().Agents["c2"].Status; got != string(status.Parked) {
		t.Errorf("after a failed wake status %q, want parked", got)
	}
}

// TestACPDriver drives one acp agent next to one tmux agent through spawn,
// refresh, send, answer, park, wake and close. The host is a script that
// records its argv and sleeps; a stub server stands in for its socket.
func TestACPDriver(t *testing.T) {
	log := tmuxLog(t)
	home := t.TempDir()
	noPlaybook(t, home)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	host := filepath.Join(home, "fakehost")
	os.WriteFile(host, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+home+"/host.args\necho --- >> "+home+"/host.args\nexec sleep 600\n"), 0o755)
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[stub]
command = "stubtool"
args = ["--x"]
acp = ["--acp"]
[yn]
command = "sh"
args = ["-c", "printf 'Approve? [y/n] '; cat"]
patterns = "fake"
`), 0o644)
	f, err := New(tc, home, host)
	if err != nil {
		t.Fatal(err)
	}
	old := Version
	Version = "v9.9.9"
	t.Cleanup(func() { Version = old })
	dir := t.TempDir()
	wt := false
	a, err := f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, "a1", "stub", dir, "do it")
	if err != nil {
		t.Fatal(err)
	}
	if a.Driver != "acp" || a.ACPServer != "stubtool --acp" || a.FleetVersion != "v9.9.9" {
		t.Errorf("board driver %q server %q version %q", a.Driver, a.ACPServer, a.FleetVersion)
	}
	if info, _ := f.Info("a1"); !strings.Contains(info, "\ndriver: acp via stubtool --acp\n") || !strings.Contains(info, "\nfleet version: v9.9.9 (started the agent)\n") {
		t.Errorf("acp info:\n%s", info)
	}
	pf := ACPPromptPath(home, "a1")
	want := tools.ShellCommand([]string{host, "acp-host", "a1", "--dir", dir, "--prompt-file", pf, "--", "stubtool", "--x", "--acp"})
	if got := startCommand(t, tc, a.PaneID); got != want {
		t.Errorf("acp window command:\n%s\nwant\n%s", got, want)
	}
	if got := hostArgs(t, home); len(got) != 1 || !reflect.DeepEqual(got[0], []string{"acp-host", "a1", "--dir", dir, "--prompt-file", pf, "--", "stubtool", "--x", "--acp"}) {
		t.Errorf("host argv %q", got)
	}
	if b, _ := os.ReadFile(pf); string(b) != "do it" {
		t.Errorf("prompt file %q", b)
	}
	if fi, err := os.Stat(pf); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("prompt file mode %v %v", fi, err)
	}
	if a.PromptAt.IsZero() {
		t.Error("an acp spawn with a task sets prompt_at")
	}

	// The tmux agent beside it launches exactly as before.
	b, err := f.SpawnWith(SpawnOpts{Worktree: &wt}, "t1", "yn", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.Driver != "" || b.ACPServer != "" {
		t.Errorf("tmux agent driver %q server %q", b.Driver, b.ACPServer)
	}
	if info, _ := f.Info("t1"); !strings.Contains(info, "\ndriver: tmux\n") {
		t.Errorf("tmux info:\n%s", info)
	}
	if got, want := startCommand(t, tc, b.PaneID), tools.ShellCommand([]string{"sh", "-c", "printf 'Approve? [y/n] '; cat"}); got != want {
		t.Errorf("tmux window command:\n%s\nwant\n%s", got, want)
	}

	// Refresh: status from the state file, line and session from the
	// transcript, and never a capture of the acp pane.
	stub := &stubHost{exitAfter: 10 * time.Millisecond}
	stub.serve(t, home, "a1")
	writeTranscript(t, home, "a1",
		acphost.Entry{Kind: acphost.KindSession, SessionID: "S1"},
		acphost.Entry{Kind: acphost.KindUser, Text: "do it"},
		acphost.Entry{Kind: acphost.KindAgent, Text: "on it\nhello there"})
	os.WriteFile(log, nil, 0o644)
	for _, st := range []status.State{
		{Status: status.Working, Event: "acp:prompt"},
		{Status: status.Waiting, WaitingFor: "Run tests?: Allow / Reject", Event: "acp:permission"},
		{Status: status.Idle, Event: "acp:end_turn"},
	} {
		st.At = time.Now()
		status.WriteState(status.StateDir(home), "a1", st)
		as, err := f.Refresh()
		if err != nil {
			t.Fatal(err)
		}
		got := f.Store.Load().Agents["a1"]
		if got.Status != string(st.Status) || got.SessionID != "S1" || len(as) != 2 {
			t.Errorf("refresh: status %q session %q want %q S1", got.Status, got.SessionID, st.Status)
		}
		line := "hello there"
		if st.WaitingFor != "" {
			line = st.WaitingFor
		}
		if got.LastLine != line {
			t.Errorf("last line %q want %q", got.LastLine, line)
		}
	}
	if n := captures(t, log, a.PaneID); n != 0 {
		t.Errorf("refresh captured the acp pane %d times", n)
	}
	if n := captures(t, log, b.PaneID); n != 3 {
		t.Errorf("refresh captured the tmux pane %d times, want 3", n)
	}
	if p, err := f.Peek("a1", 0); err != nil || !strings.Contains(p, "hello there") {
		t.Errorf("peek of an acp agent: %q %v", p, err)
	}
	if n := captures(t, log, a.PaneID); n != 0 {
		t.Errorf("peek captured the acp pane %d times", n)
	}

	// Send and answer go over the socket.
	if err := f.Send("a1", "more"); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	stub.question = &acphost.Question{Title: "Run tests?", Options: []acphost.Option{
		{ID: "ok", Name: "Allow", Kind: "allow_once"}, {ID: "no", Name: "Reject", Kind: "reject_once"}}}
	stub.mu.Unlock()
	if m, ok, err := f.Menu("a1"); err != nil || !ok || m.Question != "Run tests?" || m.Options[1] != "2. Reject" {
		t.Errorf("menu %+v %v %v", m, ok, err)
	}
	// One state read per answer: the option id comes from the question it answers.
	if c, err := f.Answer("a1", "n", "tests"); err != nil || c.Label != "2. Reject" {
		t.Errorf("answer n: %+v %v", c, err)
	}
	if _, err := f.Answer("a1", "y", ""); err == nil {
		t.Error("answer with no open question")
	}
	if err := f.SendKeys("a1", "", []string{"Escape"}); err != nil {
		t.Error(err)
	}
	if err := f.SendKeys("a1", "half", nil); err == nil {
		t.Error("text without Enter to an acp agent should be refused")
	}
	if got, want := stub.ops(), []string{"prompt:more", "state", "state", "answer:no", "state", "cancel"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ops %q want %q", got, want)
	}
	if n := captures(t, log, a.PaneID); n != 0 {
		t.Errorf("send/answer captured the acp pane %d times", n)
	}

	// Park stops the host, then kills the window; wake starts a new host on
	// the recorded session.
	stub.reset()
	if err := f.Park("a1", ""); err != nil {
		t.Fatal(err)
	}
	if got := untilStop(stub.ops()); !reflect.DeepEqual(got, []string{"state", "stop"}) {
		t.Errorf("park ops %q", got)
	}
	if f.paneOf("a1") != "" {
		t.Error("park left the pane")
	}
	w, err := f.Wake("a1", true)
	if err != nil {
		t.Fatal(err)
	}
	// The stub host never sent spawn's prompt, so the file is still there
	// and the woken host gets it: an unsent prompt is not lost.
	want = tools.ShellCommand([]string{host, "acp-host", "a1", "--dir", dir, "--session", "S1", "--prompt-file", pf, "--", "stubtool", "--x", "--acp"})
	if got := startCommand(t, tc, w.PaneID); got != want || w.Driver != "acp" {
		t.Errorf("wake window command (driver %q):\n%s\nwant\n%s", w.Driver, got, want)
	}
	stub.serve(t, home, "a1") // the woken host

	stub.reset()
	if err := f.Close("a1"); err != nil {
		t.Fatal(err)
	}
	if got := untilStop(stub.ops()); !reflect.DeepEqual(got, []string{"stop"}) {
		t.Errorf("close ops %q", got)
	}

	// The tmux agent's send still pastes into its pane.
	if err := f.Send("t1", "hello"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if out, _ := f.Peek("t1", 0); !strings.Contains(out, "hello") {
		t.Errorf("tmux send: %q", out)
	}
}

func TestACPCloseWithoutHost(t *testing.T) {
	home := t.TempDir()
	f := &Fleet{Home: home}
	f.acpStop("nobody") // no socket: not an error, nothing to log as one
	if _, err := f.acpCall("nobody", acphost.Request{Op: acphost.OpState}); !errors.Is(err, acphost.ErrNoHost) {
		t.Errorf("err %v", err)
	}
}

func TestReadTranscriptTail(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(acphost.Dir(home), 0o700)
	writeTranscript(t, home, "a1", acphost.Entry{Kind: acphost.KindSession, SessionID: "OLD"})
	big := strings.Repeat("x", 1000)
	for i := 0; i < 100; i++ {
		writeTranscript(t, home, "a1", acphost.Entry{Kind: acphost.KindAgent, Text: big})
	}
	writeTranscript(t, home, "a1", acphost.Entry{Kind: acphost.KindTool, Title: "go test", Status: "completed"})
	p := acphost.TranscriptPath(home, "a1")
	if tr := readTranscript(p, false); tr.SessionID != "" || tr.Line != "• go test [completed]" {
		t.Errorf("tail: session %q line %q", tr.SessionID, tr.Line)
	}
	if tr := readTranscript(p, true); tr.SessionID != "OLD" {
		t.Errorf("full read: session %q", tr.SessionID)
	}
}

// Stop waits until the host is gone, so its session/close finishes before
// fleet kills the window, and gives up after ACPStopWait.
func TestACPStopWaitsForHostExit(t *testing.T) {
	home := t.TempDir()
	f := &Fleet{Home: home}
	(&stubHost{exitAfter: 300 * time.Millisecond}).serve(t, home, "a1")
	start := time.Now()
	f.acpStop("a1")
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Errorf("stop returned after %v, before the host exited", d)
	}
	if _, err := f.acpCall("a1", acphost.Request{Op: acphost.OpState}); !errors.Is(err, acphost.ErrNoHost) {
		t.Errorf("host still answers: %v", err)
	}

	old := ACPStopWait
	ACPStopWait = 200 * time.Millisecond
	t.Cleanup(func() { ACPStopWait = old })
	(&stubHost{}).serve(t, home, "a2") // never exits
	start = time.Now()
	f.acpStop("a2")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("stop of a host that never exits took %v", d)
	}
}

// A park before the first refresh still records the session, from the
// transcript, so the agent can be woken.
func TestParkACPReadsSessionFromTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
	f, err := New(&tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}, home, "/bin/fleet")
	if err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["a1"] = &board.Agent{Name: "a1", Profile: "fake", Status: "idle", Driver: board.DriverACP}
		return nil
	})
	os.MkdirAll(acphost.Dir(home), 0o700)
	writeTranscript(t, home, "a1", acphost.Entry{Kind: acphost.KindSession, SessionID: "S9"})
	if err := f.Park("a1", ""); err != nil {
		t.Fatal(err)
	}
	if a := f.Store.Load().Agents["a1"]; a.SessionID != "S9" || a.Status != "parked" {
		t.Errorf("parked %q session %q", a.Status, a.SessionID)
	}
}

// The control socket's path must fit sun_path, so an acp spawn under a long
// fleet home is refused up front, not by a host that dies in its window.
func TestACPSpawnRefusesLongSocketPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), strings.Repeat("h", 100))
	os.MkdirAll(home, 0o700)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
	f, err := New(&tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}, home, "/bin/fleet")
	if err != nil {
		t.Fatal(err)
	}
	wt := false
	_, err = f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, "a1", "fake", t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "socket path") {
		t.Fatalf("spawn under a long home: %v", err)
	}
	if _, ok := f.Store.Load().Agents["a1"]; ok {
		t.Error("refused spawn left a board entry")
	}
}

func TestDriverLine(t *testing.T) {
	copilot := tools.Profile{Command: "copilot", ACP: []string{"--acp"}}
	for _, c := range []struct {
		a    board.Agent
		want string
	}{
		{board.Agent{}, "tmux"}, // no driver field: spawned before v1.0.0
		{board.Agent{Driver: board.DriverACP, ACPServer: "claude-agent-acp@0.1.0"}, "acp via claude-agent-acp@0.1.0"},
		{board.Agent{Driver: board.DriverACP}, "acp via copilot --acp"}, // started before v1.2.0: the profile's server
	} {
		if got := DriverLine(&c.a, copilot); got != c.want {
			t.Errorf("%+v: %q, want %q", c.a, got, c.want)
		}
	}
	if got := DriverLine(&board.Agent{Driver: board.DriverACP}, tools.Profile{}); got != "acp" {
		t.Errorf("unknown profile: %q", got)
	}
}

// A park of an acp agent waits up to ACPStopWait for the host before it
// kills the pane. A refresh in that window must not read the stopping
// host's idle state over "parked", nor put the pane back on the entry: the
// watcher did both live, and the agent then went gone and could not wake.
// Past the window a live pane is a park that did not take and is read.
func TestRefreshDuringACPParkKeepsParked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
	f, err := New(&tmux.Client{Socket: "fleet-test-unused"}, home, "/bin/fleet")
	if err != nil {
		t.Fatal(err)
	}
	status.WriteState(status.StateDir(home), "p1", status.State{Status: status.Idle, At: time.Now(), Event: "acp:end_turn"})
	now := time.Now()
	a := &board.Agent{Name: "p1", Driver: board.DriverACP, Status: string(status.Parked), StatusSince: now.Add(-time.Second), SessionID: "S1"}
	if got := f.classify(a, true, false, "%9", now); got != string(status.Parked) || a.PaneID != "" {
		t.Errorf("mid-park refresh: status %q pane %q, want parked and no pane", got, a.PaneID)
	}
	a.StatusSince = now.Add(-ACPStopWait - parkSettle - time.Second)
	if got := f.classify(a, true, false, "%9", now); got == string(status.Parked) || a.PaneID != "%9" {
		t.Errorf("stale parked with a live pane: status %q pane %q, want it read fresh", got, a.PaneID)
	}
}
