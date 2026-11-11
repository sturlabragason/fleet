package mcp

// Hot reload (DESIGN §6.9, ADR-25): before each request the server checks
// whether its executable changed on disk. If so, and the new binary passes
// `fleet mcp --probe`, it hands the unread input to the new binary through a
// pending file and execs it in place: same pid, same stdio pipes, so the MCP
// client never sees a disconnect.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sysx"
)

const (
	envPending  = "FLEET_MCP_PENDING"
	envReloaded = "FLEET_MCP_RELOADED"
	// maxLine is the longest request line we accept (as bufio.Scanner did).
	maxLine = 16 << 20
	// probeRetry is how long a failed probe or exec of one file identity
	// blocks another try, so a broken build doesn't slow every call.
	probeRetry = 10 * time.Second
	// reloadedNote ends the first tool result after a reload.
	reloadedNote = "\n\n[fleet updated: tools reloaded]"
)

// listChanged tells the client to refetch tools/list.
const listChanged = `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}` + "\n"

// ProbeCheck is what `fleet mcp --probe` verifies: the binary starts and has
// tool definitions.
func ProbeCheck() error {
	if len(toolDefs) == 0 || len(agentToolDefs) == 0 {
		return errors.New("no tool definitions")
	}
	return nil
}

// ResumeInput returns the input to serve. After a reload (FLEET_MCP_RELOADED=1)
// that is the request bytes the old process had already read, then stdin. It
// clears both reload env vars so children don't inherit them, and deletes the
// pending file. The bool reports whether this process is a reload.
func ResumeInput(stdin io.Reader) (io.Reader, bool) {
	if os.Getenv(envReloaded) != "1" {
		return stdin, false
	}
	path := os.Getenv(envPending)
	os.Unsetenv(envReloaded)
	os.Unsetenv(envPending)
	log := logx.For("mcp")
	log.Info("reloaded", "build", buildID(), "pid", os.Getpid())
	if path == "" {
		return stdin, true
	}
	data, err := os.ReadFile(path)
	os.Remove(path)
	if err != nil {
		log.Warn("reload pending input", "err", err)
		return stdin, true
	}
	pr := bytes.NewReader(data)
	return &resumedInput{Reader: io.MultiReader(pr, stdin), data: data, pr: pr}, true
}

// resumedInput is the pending input of a reload followed by stdin. Serve keeps
// it so a second reload can save the part of data not read yet.
type resumedInput struct {
	io.Reader
	data []byte
	pr   *bytes.Reader // reads data; Len() is the unread rest
}

// unread returns the pending bytes the reader hasn't handed out yet.
func (r *resumedInput) unread() []byte {
	if r == nil || r.pr == nil {
		return nil
	}
	return r.data[len(r.data)-r.pr.Len():]
}

// initReload fills in reload defaults and remembers the executable's identity.
// Reload is off when the executable can't be found.
func (s *Server) initReload() {
	if s.ExePath == "" && s.F != nil {
		s.ExePath = s.F.Exe
	}
	if s.Probe == nil {
		s.Probe = probe
	}
	if s.Exec == nil {
		s.Exec = sysx.Exec
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.ExePath == "" {
		return
	}
	// The running inode, not whatever sits at the path now: the binary may
	// have been replaced between exec and here.
	self := s.selfExe
	if self == "" {
		self = "/proc/self/exe"
	}
	if fi, err := os.Stat(self); err == nil {
		s.exeInfo = fi
	} else if fi, err := os.Stat(s.ExePath); err == nil {
		s.exeInfo = fi
	}
}

// sameFile reports whether a and b are the same file with the same content
// stamp (inode, modtime, size).
func sameFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.ModTime().Equal(b.ModTime()) && a.Size() == b.Size()
}

// maybeReload execs the new binary when the executable changed and the new
// one passes the probe. line is the raw request just read (with its newline)
// and br holds any read-ahead bytes. It returns true only when a (stubbed)
// Exec returned without error; a real exec never returns on success. On any
// failure the caller handles the request with the current code.
func (s *Server) maybeReload(line []byte, br *bufio.Reader, w io.Writer) bool {
	if s.exeInfo == nil {
		return false
	}
	fi, err := os.Stat(s.ExePath)
	if err != nil || sameFile(fi, s.exeInfo) {
		return false
	}
	if sameFile(fi, s.failInfo) && s.Now().Sub(s.failAt) < probeRetry {
		return false
	}
	log := logx.For("mcp")
	if err := s.Probe(s.ExePath); err != nil {
		log.Warn("reload probe failed; keeping the old code", "exe", s.ExePath, "err", err)
		s.failInfo, s.failAt = fi, s.Now()
		return false
	}
	pending, err := s.writePending(line, br)
	if err != nil {
		log.Warn("reload pending file", "err", err)
		s.failInfo, s.failAt = fi, s.Now()
		return false
	}
	// A build may have landed after the probe; never exec an unprobed file.
	if now, err := os.Stat(s.ExePath); err != nil || !sameFile(now, fi) {
		log.Info("reload skipped: binary changed after the probe", "exe", s.ExePath)
		os.Remove(pending)
		return false
	}
	if f, ok := w.(interface{ Flush() error }); ok {
		f.Flush()
	}
	env := append(reloadEnv(os.Environ()), envPending+"="+pending, envReloaded+"=1")
	if s.served != "" {
		env = append(env, envServed+"="+s.served)
	}
	log.Info("reloading", "exe", s.ExePath, "old_build", buildID(), "new_mtime", fi.ModTime(), "pid", os.Getpid())
	if err := s.Exec(s.ExePath, os.Args, env); err != nil {
		log.Error("reload exec failed; keeping the old code", "exe", s.ExePath, "err", err)
		os.Remove(pending)
		s.failInfo, s.failAt = fi, s.Now()
		return false
	}
	return true
}

// writePending saves line, br's read-ahead bytes and the unread rest of a
// previous reload's pending input to <home>/run/ (0600). The order is the
// read order: br never holds stdin bytes while pending input is left.
func (s *Server) writePending(line []byte, br *bufio.Reader) (string, error) {
	home := logx.Home()
	if s.F != nil && s.F.Home != "" {
		home = s.F.Home
	}
	dir := filepath.Join(home, "run")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "mcp-pending-*")
	if err != nil {
		return "", err
	}
	ahead, _ := br.Peek(br.Buffered())
	_, err = f.Write(line)
	if err == nil {
		_, err = f.Write(ahead)
	}
	if err == nil {
		_, err = f.Write(s.pending.unread())
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// reloadEnv drops stale reload vars from env.
func reloadEnv(env []string) []string {
	out := make([]string, 0, len(env)+3)
	for _, e := range env {
		if strings.HasPrefix(e, envPending+"=") || strings.HasPrefix(e, envReloaded+"=") || strings.HasPrefix(e, envServed+"=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// probe runs `<path> mcp --probe` and expects "ok" within 5 s.
func probe(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "mcp", "--probe")
	cmd.Env = reloadEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("%w: stdout %q stderr %q", err, strings.TrimSpace(string(out)), strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("probe said %q", strings.TrimSpace(string(out)))
	}
	return nil
}

// readLine reads one line (with its newline) of at most maxLine bytes.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		frag, err := br.ReadSlice('\n')
		buf = append(buf, frag...)
		if len(buf) > maxLine {
			return nil, bufio.ErrTooLong
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}

// buildID names the running build for the log.
func buildID() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var rev, at, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev == "" {
		return "dev"
	}
	return rev + dirty + " " + at
}

// staleNote follows reloadedNote for the admiral: tmux options, the watcher
// and the overview take a new build by themselves, ACP hosts do not
// (ADR-111). An agent's own server says nothing; it can't restart its host.
func (s *Server) staleNote() string {
	if s.Agent != "" || s.F == nil {
		return ""
	}
	names := s.F.StaleHosts()
	if len(names) == 0 {
		return ""
	}
	return "\n[still on the old build: ACP agents " + strings.Join(names, ", ") +
		". Restart each when it is idle and the user is not typing in it: fleet_park, then fleet_park action=wake.]"
}
