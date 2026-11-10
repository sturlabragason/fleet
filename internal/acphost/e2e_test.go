package acphost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/status"
)

var fakeBin string

func TestMain(m *testing.M) {
	os.Setenv("FLEET_CONFIG", "/nonexistent/fleet-config.toml")
	dir, err := os.MkdirTemp("", "acphost-fake")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeBin = filepath.Join(dir, "fakeagent")
	build := exec.Command("go", "build", "-o", fakeBin, "github.com/sturlabragason/fleet/cmd/fakeagent")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fakeagent: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// e2e runs Run against `fakeagent --acp` in a temp fleet home.
type e2e struct {
	t     *testing.T
	home  string
	state string
	out   *syncBuf
	errC  chan error
}

func newE2E(t *testing.T) *e2e {
	home, err := os.MkdirTemp("", "ah") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	return &e2e{t: t, home: home, state: filepath.Join(home, "fakestate")}
}

func (e *e2e) start(session string, copilotEvents bool, fakeArgs ...string) {
	e.t.Helper()
	e.out, e.errC = &syncBuf{}, make(chan error, 1)
	argv := append([]string{fakeBin, "--acp", "--state", e.state}, fakeArgs...)
	cfg := Config{Home: e.home, Name: "a1", Dir: e.t.TempDir(), Argv: argv, Env: os.Environ(),
		SessionID: session, CopilotEvents: copilotEvents, Out: e.out}
	sessions := len(e.entries(KindSession))
	ctx, cancel := context.WithCancel(context.Background())
	fin := make(chan struct{})
	go func() { e.errC <- Run(ctx, cfg); close(fin) }()
	e.t.Cleanup(func() {
		cancel()
		select {
		case <-fin:
		case <-time.After(15 * time.Second):
			e.t.Error("host did not stop")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for len(e.entries(KindSession)) == sessions {
		if time.Now().After(deadline) {
			e.t.Fatalf("no session entry\nout:\n%s", e.out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.waitStatus("idle") // Ready
}

func (e *e2e) call(req Request) Response {
	e.t.Helper()
	r, err := Call(e.home, "a1", req, 5*time.Second)
	if err != nil {
		e.t.Fatalf("%s: %v\nout:\n%s", req.Op, err, e.out.String())
	}
	return r
}

func (e *e2e) status() status.State {
	b, _ := os.ReadFile(filepath.Join(status.StateDir(e.home), "a1.json"))
	var s status.State
	_ = json.Unmarshal(b, &s)
	return s
}

func (e *e2e) waitStatus(want string) status.State {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := e.status(); string(s.Status) == want {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("status %q never came (now %+v)\nout:\n%s", want, e.status(), e.out.String())
	return status.State{}
}

func (e *e2e) exit() error {
	e.t.Helper()
	select {
	case err := <-e.errC:
		return err
	case <-time.After(10 * time.Second):
		e.t.Fatal("host did not exit")
		return nil
	}
}

func (e *e2e) entries(kind string) []Entry {
	var out []Entry
	for _, x := range Tail(TranscriptPath(e.home, "a1"), 1000) {
		if x.Kind == kind {
			out = append(out, x)
		}
	}
	return out
}

func TestE2EPromptAskCancelStopResume(t *testing.T) {
	e := newE2E(t)
	e.start("", true, "--resume")

	// prompt → working → idle
	if r := e.call(Request{Op: OpPrompt, Text: "hello [slow 50]"}); r.Status != "working" {
		t.Fatalf("after prompt: %+v", r)
	}
	e.waitStatus("idle")
	agent := e.entries(KindAgent)
	if len(agent) == 0 || agent[len(agent)-1].Text != "fake: hello [slow 50]" {
		t.Fatalf("agent entries = %+v", agent)
	}

	// [ask] → waiting with options → answer → idle
	e.call(Request{Op: OpPrompt, Text: "do it [ask]"})
	s := e.waitStatus("waiting")
	if s.WaitingFor == "" {
		t.Fatalf("waiting without WaitingFor: %+v", s)
	}
	q := e.call(Request{Op: OpState}).Question
	if q == nil || len(q.Options) != 2 || q.Options[0].Name != "Allow" || q.Options[1].Name != "Reject" {
		t.Fatalf("question = %+v", q)
	}
	e.call(Request{Op: OpAnswer, Option: 1})
	e.waitStatus("idle")
	if a := e.entries(KindAnswer); len(a) != 1 || a[0].Text != "Allow" {
		t.Fatalf("answers = %+v", a)
	}

	// [hang] + cancel → idle
	e.call(Request{Op: OpPrompt, Text: "wait [hang]"})
	time.Sleep(100 * time.Millisecond)
	if st := e.status().Status; st != "working" {
		t.Fatalf("hang status = %q", st)
	}
	e.call(Request{Op: OpCancel})
	e.waitStatus("idle")

	// stop → exit 0, session id kept in the transcript
	sid := LastSession(TranscriptPath(e.home, "a1"))
	if !strings.HasPrefix(sid, "fake-") {
		t.Fatalf("session id = %q", sid)
	}
	e.call(Request{Op: OpStop})
	if err := e.exit(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// restart with --resume offered: resumed, nothing replayed
	e.start(sid, false, "--resume")
	sess := e.entries(KindSession)
	if last := sess[len(sess)-1]; last.SessionID != sid || last.Text != "resumed" {
		t.Fatalf("session entry = %+v", last)
	}
	if strings.Contains(e.out.String(), "fake: hello") {
		t.Fatalf("resume replayed:\n%s", e.out.String())
	}
	e.call(Request{Op: OpStop})
	if err := e.exit(); err != nil {
		t.Fatal(err)
	}

	// restart without resume: loaded, replay drawn, not recorded again
	before := len(e.entries(KindAgent))
	e.start(sid, false)
	sess = e.entries(KindSession)
	if last := sess[len(sess)-1]; last.SessionID != sid || last.Text != "loaded" {
		t.Fatalf("session entry = %+v", last)
	}
	if !strings.Contains(e.out.String(), "fake: hello [slow 50]") {
		t.Fatalf("load replay not drawn:\n%s", e.out.String())
	}
	if after := len(e.entries(KindAgent)); after != before {
		t.Fatalf("replay recorded: %d agent entries, was %d", after, before)
	}
	e.call(Request{Op: OpStop})
	if err := e.exit(); err != nil {
		t.Fatal(err)
	}
}

func TestE2ECrashIsError(t *testing.T) {
	e := newE2E(t)
	e.start("", false)
	e.call(Request{Op: OpPrompt, Text: "boom [crash]"})
	err := e.exit()
	if err == nil || !strings.Contains(err.Error(), "exit") {
		t.Fatalf("run after crash = %v", err)
	}
	if st := e.status().Status; st != "error" {
		t.Fatalf("status = %q", st)
	}
	errs := e.entries(KindError)
	if len(errs) == 0 || !strings.Contains(errs[len(errs)-1].Text, "agent exited") {
		t.Fatalf("error entries = %+v", errs)
	}
	if _, err := Call(e.home, "a1", Request{Op: OpState}, time.Second); err == nil {
		t.Fatal("socket still answers after the host exited")
	}
}

func TestE2EPromptErrorAndSubagentEvents(t *testing.T) {
	e := newE2E(t)
	e.start("", true)
	e.call(Request{Op: OpPrompt, Text: "x [subagent]"})
	e.waitStatus("idle")
	e.call(Request{Op: OpPrompt, Text: "x [error]"})
	e.waitStatus("error")
	if errs := e.entries(KindError); len(errs) == 0 || !strings.HasPrefix(errs[0].Text, "prompt: ") {
		t.Fatalf("error entries = %+v", errs)
	}
	// The host keeps running after a failed prompt: the next one works.
	e.call(Request{Op: OpPrompt, Text: "again"})
	e.waitStatus("idle")
}
