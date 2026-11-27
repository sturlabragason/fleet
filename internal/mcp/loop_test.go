package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func worktreesCall(id int) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
		"name": "fleet_worktrees", "arguments": map[string]any{"dir": "/r"}}}
}

// blockingCLI makes the first CLI call block until its context ends or
// release closes; later calls return at once. ended gets the first call's
// ctx error, runs counts the calls.
func blockingCLI(s *Server) (started, release chan struct{}, ended chan error, runs *int) {
	started, release, ended, runs = make(chan struct{}), make(chan struct{}), make(chan error, 1), new(int)
	s.RunCLI = func(ctx context.Context, _ string, _ ...string) (string, int, error) {
		if *runs++; *runs > 1 {
			return "second", 0, nil
		}
		close(started)
		select {
		case <-ctx.Done():
		case <-release:
		case <-time.After(10 * time.Second):
		}
		ended <- ctx.Err()
		return "listed", 0, nil
	}
	return started, release, ended, runs
}

// Two requests in flight, one running and one queued behind it: a cancel
// by id ends only that one. A queued call that is cancelled never runs.
func TestCancelEndsOnlyItsCall(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cancel   int
		want     string
		runs     int
		firstEnd bool // the running call's context ended
	}{
		{"cancel the running call", 1, "2 3", 2, true},
		{"cancel the queued call", 2, "1 3", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := cliServer(t, "", 0)
			started, release, ended, runs := blockingCLI(s)
			r := startRPC(t, s)
			r.send(worktreesCall(1))
			<-started
			r.send(worktreesCall(2))
			r.send(cancelMsg(tc.cancel))
			go func() {
				r.send(pingMsg(3)) // returns once the reader took the cancel
				close(release)
			}()
			ids, _ := r.replies("3")
			if strings.Join(ids, " ") != tc.want {
				t.Fatalf("replies %v, want %s", ids, tc.want)
			}
			if err := <-ended; (err != nil) != tc.firstEnd {
				t.Fatalf("running call's ctx %v, ended want %v", err, tc.firstEnd)
			}
			if *runs != tc.runs {
				t.Fatalf("%d calls ran, want %d", *runs, tc.runs)
			}
		})
	}
}

// A response for an id fleet never sent is ignored (and logged): no reply,
// and it does not end a fleet_wait.
func TestStrayResponseIgnored(t *testing.T) {
	fastWait(t)
	r := startRPC(t, NewAgent(mailHome(t), "a"))
	r.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": "fleet_wait", "arguments": map[string]any{"timeout_s": 1}}})
	r.send(map[string]any{"jsonrpc": "2.0", "id": "nobody", "result": map[string]any{}})
	ids, text := r.replies("1")
	if len(ids) != 1 || !strings.HasPrefix(text["1"], "timeout after") {
		t.Fatalf("replies %v, wait %q: want only the wait, timed out", ids, text["1"])
	}
	r.send(pingMsg(2))
	if ids, _ := r.replies("2"); len(ids) != 1 {
		t.Fatalf("replies %v, want only the ping", ids)
	}
}

// waitInFlight returns once the reader dispatched a call.
func waitInFlight(t *testing.T, s *Server) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; runtime.Gosched() {
		s.wmu.Lock()
		n := s.flight
		s.wmu.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no call in flight")
		}
	}
}

// watchExe makes s reload from a fake executable whose probe passes, and
// records the pending input of each exec.
func watchExe(t *testing.T, s *Server) (exe string, execs chan []byte) {
	t.Helper()
	exe = filepath.Join(t.TempDir(), "fleet")
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.ExePath, s.selfExe = exe, exe
	s.Probe = func(string) error { return nil }
	execs = make(chan []byte, 1)
	s.Exec = execRecorder(execs)
	return exe, execs
}

// A reload with a call in flight lets it finish and answer first (a
// fleet_wait is ended by the line), then hands over exactly the line that
// triggered it: nothing dropped, nothing served twice.
func TestReloadWithCallInFlight(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start func(t *testing.T, s *Server, r *rpc) (release func())
		want  string // the call's reply starts with this
	}{
		{"fleet_wait", func(t *testing.T, s *Server, r *rpc) func() {
			t.Helper()
			r.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
				"name": "fleet_wait", "arguments": map[string]any{"timeout_s": 20}}})
			waitInFlight(t, s)
			return func() {}
		}, "interrupted after"},
		{"slow tool", func(t *testing.T, s *Server, r *rpc) func() {
			t.Helper()
			started, release, _, _ := blockingCLI(s)
			r.send(worktreesCall(1))
			<-started
			return func() { close(release) }
		}, "listed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastWait(t)
			s, _ := cliServer(t, "", 0)
			exe, execs := watchExe(t, s)
			r := startRPC(t, s)
			release := tc.start(t, s, r)
			replaceExe(t, exe, "v2")
			r.send(pingMsg(2)) // returns once read: the reload now waits for call 1
			release()
			if ids, text := r.replies("1"); len(ids) != 1 || !strings.HasPrefix(text["1"], tc.want) {
				t.Fatalf("replies %v, call %q, want %q…", ids, text["1"], tc.want)
			}
			if got := <-execs; string(got) != string(mustJSON(pingMsg(2)))+"\n" {
				t.Fatalf("pending %q", got)
			}
		})
	}
}

// A call waiting for its dialog's answer needs the reader, so the reload
// waits: the line is served by the current code, and a later line reloads.
func TestReloadWaitsForDialog(t *testing.T) {
	s := elicitServer(t)
	exe, execs := watchExe(t, s)
	r := startRPC(t, s)
	r.send(closeCall(1, nil))
	id := dialog(r)
	replaceExe(t, exe, "v2")
	r.send(pingMsg(2))
	r.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"action": "accept", "content": map[string]any{"confirm": true}}})
	if ids, text := r.replies("2"); strings.Join(ids, " ") != "1 2" || !strings.Contains(text["1"], "closed a") {
		t.Fatalf("replies %v, close %q", ids, text["1"])
	}
	select {
	case got := <-execs:
		t.Fatalf("reloaded with a dialog open, pending %q", got)
	default:
	}
	r.send(pingMsg(3))
	if got := <-execs; string(got) != string(mustJSON(pingMsg(3)))+"\n" {
		t.Fatalf("pending %q", got)
	}
}

// A wait interrupted by a line while it is queued, then cancelled by the
// client, gets no reply: the cancel wins over the earlier interrupt.
func TestCancelAfterInterrupt(t *testing.T) {
	fastWait(t)
	s, _ := cliServer(t, "", 0)
	started, release, _, _ := blockingCLI(s)
	r := startRPC(t, s)
	r.send(worktreesCall(1))
	<-started
	r.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "fleet_wait", "arguments": map[string]any{"timeout_s": 20}}})
	r.send(pingMsg(3)) // interrupts the queued wait
	r.send(cancelMsg(2))
	go func() {
		r.send(pingMsg(4)) // returns once the reader took the cancel
		close(release)
	}()
	if ids, _ := r.replies("4"); strings.Join(ids, " ") != "1 3 4" {
		t.Fatalf("replies %v, want 1 3 4 (no reply to the cancelled wait)", ids)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("stdout gone") }

// A reply that fails to write while Serve finishes at EOF is Serve's error.
func TestServeReturnsWriteErrorAtEOF(t *testing.T) {
	s, _ := cliServer(t, "", 0)
	if err := s.Serve(strings.NewReader(string(mustJSON(pingMsg(1)))+"\n"), failWriter{}); err == nil || !strings.Contains(err.Error(), "stdout gone") {
		t.Fatalf("Serve = %v, want the write error", err)
	}
}
