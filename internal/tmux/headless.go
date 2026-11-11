package tmux

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// Headless is true where there is no tmux (Windows), or when FLEET_HEADLESS=1
// asks for it. Run then answers the tmux commands fleet's agent paths use from
// a small in-process stand-in: a "pane" is a detached process whose output
// goes to a log file, and its options live in a JSON file next to it. That is
// enough for acp agents, which fleet never reads the screen of: spawn, ls,
// status, send, close and the MCP tools. It is not a terminal multiplexer:
// there is no screen, no keys and no layout, so the overview, the wall and
// tmux-driver agents do not work headless (DESIGN §7d, ADR-117).
var Headless = runtime.GOOS == "windows" || os.Getenv("FLEET_HEADLESS") == "1"

// ErrHeadless is returned for tmux commands the headless stand-in can't do.
var ErrHeadless = errors.New("not available without tmux (headless: acp agents only)")

type hlPane struct {
	ID       string            `json:"id"`
	Session  string            `json:"session"`
	Window   string            `json:"window"`
	WindowID string            `json:"window_id"`
	PID      int               `json:"pid"`
	Cmd      string            `json:"cmd"`
	Start    string            `json:"start"`
	Log      string            `json:"log"`
	Opts     map[string]string `json:"opts,omitempty"`
}

type hlState struct {
	Next     int               `json:"next"`
	Sessions []string          `json:"sessions"`
	Panes    []*hlPane         `json:"panes"`
	Opts     map[string]string `json:"opts,omitempty"`
}

// hlDir is where the stand-in keeps a socket's state and pane logs.
func (c *Client) hlDir() string {
	d := os.Getenv("TMUX_TMPDIR")
	if d == "" {
		d = os.TempDir()
	}
	return filepath.Join(d, "fleet-headless", c.Socket)
}

// headlessRun runs one tmux command against the stand-in's state, under a
// file lock so concurrent fleet processes see one server.
func (c *Client) headlessRun(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	dir := c.hlDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	lf, err := os.OpenFile(filepath.Join(dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer lf.Close()
	if err := sysx.Flock(lf, sysx.LockEx); err != nil {
		return "", err
	}
	defer sysx.Flock(lf, sysx.LockUn)
	path := filepath.Join(dir, "state.json")
	st := &hlState{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Opts == nil {
		st.Opts = map[string]string{}
	}
	out, changed, err := c.hlCmd(st, dir, args[0], args[1:])
	if changed {
		b, _ := json.Marshal(st)
		tmp := path + ".tmp"
		if werr := os.WriteFile(tmp, b, 0o600); werr == nil {
			_ = os.Rename(tmp, path)
		}
	}
	if err != nil {
		err = fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimRight(out, "\n"), err
}

// hlFlags splits tmux-style args: flags with a value (valued), clusters of
// boolean flags, then the positionals.
func hlFlags(args []string, valued string) (map[byte]string, []string, []string) {
	fl := map[byte]string{}
	var envs []string
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		for j := 1; j < len(a); j++ {
			f := a[j]
			if strings.IndexByte(valued, f) < 0 {
				fl[f] = "1"
				continue
			}
			v := a[j+1:]
			if v == "" && i+1 < len(args) {
				i++
				v = args[i]
			}
			if f == 'e' {
				envs = append(envs, v)
			} else {
				fl[f] = v
			}
			break
		}
	}
	return fl, args[i:], envs
}

func (c *Client) hlCmd(st *hlState, dir, cmd string, args []string) (string, bool, error) {
	switch cmd {
	case "list-sessions", "ls":
		if len(st.Sessions) == 0 {
			return "", false, errors.New("no server running")
		}
		return strings.Join(st.Sessions, "\n"), false, nil
	case "has-session":
		fl, _, _ := hlFlags(args, "t")
		if !hasString(st.Sessions, strings.TrimPrefix(fl['t'], "=")) {
			return "", false, errors.New("can't find session")
		}
		return "", false, nil
	case "new-session", "new-window", "neww":
		return c.hlNew(st, dir, cmd != "new-session", args)
	case "list-panes", "lsp":
		fl, _, _ := hlFlags(args, "tFf")
		var b strings.Builder
		for _, p := range st.Panes {
			if fl['a'] == "" && fl['t'] != "" && !p.matches(st, fl['t']) {
				continue
			}
			b.WriteString(hlFormat(st, p, fl['F']) + "\n")
		}
		return b.String(), false, nil
	case "display-message", "display":
		fl, pos, _ := hlFlags(args, "tcdF")
		if fl['p'] == "" {
			return "", false, nil
		}
		f := fl['F']
		if len(pos) > 0 {
			f = pos[0]
		}
		var p *hlPane
		if t := fl['t']; t != "" {
			if p = st.find(t); p == nil {
				return "", false, errors.New("can't find pane: " + t)
			}
		}
		return hlFormat(st, p, f), false, nil
	case "set-option", "set", "set-window-option", "setw":
		fl, pos, _ := hlFlags(args, "t")
		if len(pos) == 0 {
			return "", false, nil
		}
		opts := st.Opts
		if fl['p'] != "" {
			p := st.find(fl['t'])
			if p == nil {
				return "", false, errors.New("can't find pane: " + fl['t'])
			}
			if p.Opts == nil {
				p.Opts = map[string]string{}
			}
			opts = p.Opts
		}
		if fl['u'] != "" {
			delete(opts, pos[0])
		} else if len(pos) > 1 {
			opts[pos[0]] = pos[1]
		}
		return "", true, nil
	case "show-options", "show":
		fl, pos, _ := hlFlags(args, "t")
		opts := st.Opts
		if fl['p'] != "" {
			p := st.find(fl['t'])
			if p == nil {
				return "", false, errors.New("can't find pane: " + fl['t'])
			}
			opts = p.Opts
		}
		if len(pos) == 0 {
			return "", false, nil
		}
		v, ok := opts[pos[0]]
		if !ok {
			return "", false, nil
		}
		if fl['v'] != "" {
			return v, false, nil
		}
		return pos[0] + " " + v, false, nil
	case "capture-pane", "capturep":
		fl, _, _ := hlFlags(args, "tSEb")
		p := st.find(fl['t'])
		if p == nil {
			return "", false, errors.New("can't find pane: " + fl['t'])
		}
		n := 50
		if s := strings.TrimPrefix(fl['S'], "-"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v > 0 {
				n = v
			}
		}
		return tailLines(p.Log, n), false, nil
	case "kill-pane", "killp":
		fl, _, _ := hlFlags(args, "t")
		return "", true, st.kill(func(p *hlPane) bool { return p.matchesPane(fl['t']) })
	case "kill-window", "killw":
		fl, _, _ := hlFlags(args, "t")
		return "", true, st.kill(func(p *hlPane) bool { return p.matches(st, fl['t']) })
	case "kill-session":
		fl, _, _ := hlFlags(args, "t")
		s := strings.TrimPrefix(fl['t'], "=")
		st.Sessions = removeString(st.Sessions, s)
		return "", true, st.kill(func(p *hlPane) bool { return p.Session == s })
	case "kill-server":
		err := st.kill(func(*hlPane) bool { return true })
		st.Sessions = nil
		return "", true, err
	case "send-keys", "send", "paste-buffer", "pasteb", "load-buffer", "loadb", "set-buffer", "setb",
		"respawn-pane", "respawnp", "attach-session", "attach", "switch-client", "switchc":
		return "", false, ErrHeadless
	}
	// Layout, focus and display commands have nothing to act on headless.
	logx.For("tmux").Debug("headless: ignored", "cmd", cmd)
	return "", false, nil
}

// hlNew is new-session and new-window: a session is a name; a window with a
// command is a detached process.
func (c *Client) hlNew(st *hlState, dir string, window bool, args []string) (string, bool, error) {
	fl, pos, envs := hlFlags(args, "stnxycFeEX")
	sess := fl['s']
	if window {
		sess = strings.TrimSuffix(strings.TrimPrefix(fl['t'], "="), ":")
		if i := strings.IndexByte(sess, ':'); i >= 0 {
			sess = sess[:i]
		}
		if !hasString(st.Sessions, sess) {
			return "", false, errors.New("can't find session: " + sess)
		}
	} else {
		if hasString(st.Sessions, sess) {
			return "", false, errors.New("duplicate session: " + sess)
		}
		st.Sessions = append(st.Sessions, sess)
	}
	if len(pos) == 0 || strings.TrimSpace(strings.Join(pos, " ")) == "" {
		// A session or window with only a shell: nothing for fleet to run.
		return "", true, nil
	}
	start := strings.Join(pos, " ")
	argv, err := shellArgv(start)
	if err != nil || len(argv) == 0 {
		return "", true, fmt.Errorf("headless: can't run %q: %v", start, err)
	}
	st.Next++
	p := &hlPane{ID: "%" + strconv.Itoa(st.Next), Session: sess, Window: fl['n'],
		WindowID: "@" + strconv.Itoa(st.Next), Start: start, Cmd: strings.TrimSuffix(filepath.Base(argv[0]), ".exe")}
	p.Log = filepath.Join(dir, "pane"+strconv.Itoa(st.Next)+".log")
	logf, err := os.Create(p.Log)
	if err != nil {
		return "", true, err
	}
	defer logf.Close()
	ex := exec.Command(argv[0], argv[1:]...)
	ex.Dir = fl['c']
	ex.Env = mergeEnv(os.Environ(), envs)
	ex.Stdout, ex.Stderr = logf, logf
	sysx.Detach(ex)
	if err := ex.Start(); err != nil {
		return "", true, err
	}
	p.PID = ex.Process.Pid
	_ = ex.Process.Release()
	st.Panes = append(st.Panes, p)
	if fl['P'] == "" {
		return "", true, nil
	}
	f := fl['F']
	if f == "" {
		f = "#{session_name}:#{window_index}.#{pane_index}"
	}
	return hlFormat(st, p, f), true, nil
}

func (st *hlState) find(t string) *hlPane {
	for _, p := range st.Panes {
		if p.matches(st, t) {
			return p
		}
	}
	return nil
}

func (p *hlPane) matchesPane(t string) bool {
	if i := strings.LastIndexByte(t, '%'); i >= 0 {
		t = t[i:]
	}
	return t == p.ID
}

// matches is a -t target: a pane id, window id, session, or session:window.
func (p *hlPane) matches(st *hlState, t string) bool {
	t = strings.TrimPrefix(t, "=")
	if p.matchesPane(t) || t == p.WindowID || t == p.Session || t == p.Session+":" {
		return true
	}
	s, w, ok := strings.Cut(t, ":")
	if !ok {
		return false
	}
	w, _, _ = strings.Cut(w, ".")
	return (s == "" || s == p.Session) && (w == p.Window || w == p.WindowID || w == "="+p.Window)
}

func (st *hlState) kill(match func(*hlPane) bool) error {
	keep := st.Panes[:0]
	found := false
	for _, p := range st.Panes {
		if !match(p) {
			keep = append(keep, p)
			continue
		}
		found = true
		if p.alive() {
			_ = sysx.KillTree(p.PID)
		}
		_ = os.Remove(p.Log)
	}
	st.Panes = keep
	if !found {
		return errors.New("can't find pane")
	}
	return nil
}

func (p *hlPane) alive() bool {
	if p.PID <= 0 {
		return false
	}
	err := sysx.Kill(p.PID, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// hlFormat expands #{name} in f for pane p (nil: server-level only).
func hlFormat(st *hlState, p *hlPane, f string) string {
	var b strings.Builder
	for {
		i := strings.Index(f, "#{")
		if i < 0 {
			b.WriteString(f)
			return b.String()
		}
		j := strings.IndexByte(f[i:], '}')
		if j < 0 {
			b.WriteString(f)
			return b.String()
		}
		b.WriteString(f[:i])
		b.WriteString(hlVar(st, p, f[i+2:i+j]))
		f = f[i+j+1:]
	}
}

func hlVar(st *hlState, p *hlPane, k string) string {
	if p != nil {
		switch k {
		case "pane_id":
			return p.ID
		case "session_name":
			return p.Session
		case "window_id":
			return p.WindowID
		case "window_name":
			return p.Window
		case "window_index", "pane_index":
			return "0"
		case "pane_pid":
			return strconv.Itoa(p.PID)
		case "pane_dead":
			if p.alive() {
				return "0"
			}
			return "1"
		case "pane_current_command":
			return p.Cmd
		case "pane_start_command":
			return p.Start
		case "pane_width", "window_width":
			return "200"
		case "pane_height", "window_height":
			return "50"
		}
		if v, ok := p.Opts[k]; ok {
			return v
		}
	}
	switch k {
	case "socket_path":
		return filepath.Join(os.TempDir(), "fleet-headless")
	case "pid":
		return strconv.Itoa(os.Getpid())
	}
	return st.Opts[k]
}

// shellArgv splits a POSIX sh command line as fleet writes them
// (tools.ShellCommand: bash -lc 'exec ...'): quotes and backslashes, no
// expansion. The bash -lc wrapper is unwrapped, as Windows has no bash to run it.
func shellArgv(s string) ([]string, error) {
	w, err := shellWords(s)
	if err != nil {
		return nil, err
	}
	if len(w) == 3 && (w[0] == "bash" || w[0] == "sh") && (w[1] == "-lc" || w[1] == "-c") {
		if w, err = shellWords(w[2]); err != nil {
			return nil, err
		}
	}
	if len(w) > 0 && w[0] == "exec" {
		w = w[1:]
	}
	return w, nil
}

func shellWords(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unterminated '")
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			in = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(`"\$`+"`", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New(`unterminated "`)
			}
			in = true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			in = true
		case c == ' ' || c == '\t' || c == '\n':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out, nil
}

// mergeEnv is base with the KEY=VAL entries of set replacing their keys.
func mergeEnv(base, set []string) []string {
	m := map[string]string{}
	var keys []string
	for _, e := range append(append([]string{}, base...), set...) {
		k, v, _ := strings.Cut(e, "=")
		uk := k
		if runtime.GOOS == "windows" {
			uk = strings.ToUpper(k)
		}
		if _, ok := m[uk]; !ok {
			keys = append(keys, uk)
		}
		m[uk] = k + "=" + v
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

func tailLines(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func hasString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func removeString(ss []string, s string) []string {
	out := ss[:0]
	for _, x := range ss {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
