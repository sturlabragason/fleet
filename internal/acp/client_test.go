package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/testenv"
)

// TestMain lets the test binary act as a tiny ACP agent for Start tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("ACP_TEST_HELPER"); mode != "" {
		helperAgent(mode)
		return
	}
	os.Exit(testenv.Main(m))
}

// helperAgent answers initialize, prints its cwd to stderr, then exits:
// "clean" on stdin EOF, "crash" with status 3 after initialize.
func helperAgent(mode string) {
	wd, _ := os.Getwd()
	fmt.Fprintln(os.Stderr, "cwd="+wd)
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var m Message
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
			continue
		}
		fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true}}}`+"\n", m.ID)
		switch mode {
		case "crash":
			os.Exit(3)
		case "orphan":
			// A grandchild inherits stdout and stderr and outlives us.
			gc := exec.Command("sleep", "5")
			gc.Stdout, gc.Stderr = os.Stdout, os.Stderr
			gc.Start()
			os.WriteFile("gc.pid", []byte(strconv.Itoa(gc.Process.Pid)), 0o600)
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func startHelper(t *testing.T, mode string) (*Client, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	stderr := &bytes.Buffer{}
	env := append(os.Environ(), "ACP_TEST_HELPER="+mode)
	cl, cmd, err := Start(context.Background(), []string{os.Args[0]}, dir, env, stderr, &recHandler{})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Process == nil {
		t.Fatal("no process")
	}
	t.Cleanup(func() { cl.Close(); cmd.Process.Kill() })
	return cl, stderr, dir
}

func TestStartCleanExit(t *testing.T) {
	cl, stderr, dir := startHelper(t, "clean")
	res, err := cl.Initialize(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.AgentCapabilities.CanLoad() {
		t.Errorf("caps %+v", res.AgentCapabilities)
	}
	cl.Close()
	waitDone(t, cl)
	if err := cl.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
	if !strings.Contains(stderr.String(), "cwd=") || !strings.Contains(stderr.String(), dir) {
		t.Errorf("stderr %q does not show dir %s", stderr.String(), dir)
	}
}

func TestStartCrash(t *testing.T) {
	cl, _, _ := startHelper(t, "crash")
	cl.Initialize(context.Background(), false)
	waitDone(t, cl)
	var ee *exec.ExitError
	if err := cl.Err(); !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("Err = %v, want exit 3", err)
	}
	if _, err := cl.NewSession(context.Background(), "/", nil); !errors.Is(err, ErrClosed) {
		t.Errorf("call after exit: %v", err)
	}
}

func TestStartOrphanHoldsPipes(t *testing.T) {
	old := exitGrace
	exitGrace = 200 * time.Millisecond
	defer func() { exitGrace = old }()
	cl, _, dir := startHelper(t, "orphan")
	t.Cleanup(func() {
		if b, err := os.ReadFile(filepath.Join(dir, "gc.pid")); err == nil {
			if pid, err := strconv.Atoi(string(b)); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	if _, err := cl.Initialize(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	waitDone(t, cl)
	if err := cl.Err(); err != nil {
		t.Errorf("Err = %v, want nil after a clean exit", err)
	}
}

func TestStartBadCommand(t *testing.T) {
	if _, _, err := Start(context.Background(), nil, "", nil, nil, nil); err == nil {
		t.Error("empty argv accepted")
	}
	if _, _, err := Start(context.Background(), []string{"/nonexistent/acp-agent"}, "", nil, nil, nil); err == nil {
		t.Error("missing binary accepted")
	}
}

func TestInitializeParams(t *testing.T) {
	for _, events := range []bool{false, true} {
		t.Run(fmt.Sprint(events), func(t *testing.T) {
			cl, p := newPair(t, &recHandler{})
			done := make(chan error, 1)
			go func() { _, err := cl.Initialize(context.Background(), events); done <- err }()
			m := p.recv()
			if m.Method != MethodInitialize {
				t.Fatalf("method %q", m.Method)
			}
			var got map[string]any
			json.Unmarshal(m.Params, &got)
			caps := got["clientCapabilities"].(map[string]any)
			if got["protocolVersion"] != float64(1) || caps["terminal"] != false {
				t.Errorf("params %s", m.Params)
			}
			fs := caps["fs"].(map[string]any)
			if fs["readTextFile"] != false || fs["writeTextFile"] != false {
				t.Errorf("fs %v", fs)
			}
			meta, hasMeta := caps["_meta"]
			if hasMeta != events {
				t.Fatalf("_meta present=%v, want %v: %s", hasMeta, events, m.Params)
			}
			if events {
				ev := meta.(map[string]any)["github.com/copilot"].(map[string]any)["events"].([]any)
				if len(ev) != len(CopilotEvents) || ev[0] != CopilotEvents[0] {
					t.Errorf("events %v", ev)
				}
			}
			p.reply(m.ID, `{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{},"close":{}}}}`)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSessionMethods(t *testing.T) {
	cl, p := newPair(t, &recHandler{})
	mcp := []MCPServer{{Name: "fleet", Command: "fleet", Args: []string{"mcp"}, Env: []EnvVariable{}}}
	calls := []struct {
		method string
		run    func() error
		check  func(json.RawMessage) bool
	}{
		{MethodSessionLoad, func() error { return cl.LoadSession(context.Background(), "s1", "/w", mcp) },
			func(p json.RawMessage) bool {
				return strings.Contains(string(p), `"sessionId":"s1"`) && strings.Contains(string(p), `"name":"fleet"`)
			}},
		{MethodSessionResume, func() error { return cl.ResumeSession(context.Background(), "s2", "/w", nil) },
			func(p json.RawMessage) bool { return strings.Contains(string(p), `"mcpServers":[]`) }},
		{MethodSessionSetMode, func() error { return cl.SetMode(context.Background(), "s4", "plan") },
			func(p json.RawMessage) bool { return string(p) == `{"sessionId":"s4","modeId":"plan"}` }},
		{MethodSessionClose, func() error { return cl.CloseSession(context.Background(), "s3") },
			func(p json.RawMessage) bool { return string(p) == `{"sessionId":"s3"}` }},
	}
	for _, c := range calls {
		errc := make(chan error, 1)
		go func() { errc <- c.run() }()
		m := p.recv()
		if m.Method != c.method || !c.check(m.Params) {
			t.Errorf("%s: got %s %s", c.method, m.Method, m.Params)
		}
		p.reply(m.ID, `null`)
		if err := <-errc; err != nil {
			t.Errorf("%s: %v", c.method, err)
		}
	}
	// session/new with no id is an error.
	errc := make(chan error, 1)
	go func() { _, err := cl.NewSession(context.Background(), "/", nil); errc <- err }()
	m := p.recv()
	p.reply(m.ID, `{}`)
	if err := <-errc; err == nil {
		t.Error("empty sessionId accepted")
	}
}
