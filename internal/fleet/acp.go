package fleet

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

// The acp driver (DESIGN §7d, ADR-74): the agent's window runs
// `fleet acp-host`, which speaks ACP to the tool and serves a control
// socket. Fleet never reads such an agent's screen: status comes from the
// state file the host writes, the last line and the session id from its
// transcript, and send, answer, park and close go over the socket.

// headless: no tmux (Windows), so every agent runs under acp, opt-in
// profiles such as claude included, and --driver tmux is refused.
var headless = tmux.Headless

// ACPCallTimeout bounds one control call to an agent's host.
var ACPCallTimeout = 5 * time.Second

// driverFor resolves a spawn's driver: the spawn's own choice, else the
// profile's and then the [defaults] driver in config.toml, else the tool
// profile's default (acp for every profile that runs copilot or agy, such
// as claude-copilot; ADR-74 (h), (i)), else tmux. A profile without an ACP
// server refuses the acp driver. An adapter profile (claude, ADR-76) is
// opt-in: [defaults] never moves it.
func (f *Fleet) driverFor(want, profile string) (string, error) {
	d, _, err := f.driverSource(want, profile)
	return d, err
}

// driverSource is driverFor with the layer that decided.
func (f *Fleet) driverSource(want, profile string) (d, src string, err error) {
	if f.Profiles[profile].ACPOptIn() {
		d, src = f.Prefs.ProfileDriver(profile), "[profile."+profile+"] (opt-in profile: [defaults] does not apply)"
	} else if d = f.Prefs.For(profile).Driver; d != "" {
		if f.Prefs.ProfileDriver(profile) == "" {
			src = "[defaults]"
		} else {
			src = "[profile." + profile + "]"
		}
	}
	if want != "" {
		if d = prefs.ParseDriver(want); d == "" {
			return "", "", fmt.Errorf("unknown driver %q (use tmux or acp)", want)
		}
		src = "--driver"
	}
	if d == "" {
		d, src = prefs.ParseDriver(f.Profiles[profile].DefaultDriver()), "tool profile's default"
	}
	if d == "" {
		d, src = prefs.DriverTmux, "built-in"
	}
	if headless && d != prefs.DriverACP {
		if src == "--driver" {
			return "", "", fmt.Errorf("the tmux driver needs tmux, which this system does not have; agents run under --driver acp here")
		}
		d, src = prefs.DriverACP, "no tmux (Windows): every agent runs under acp"
	}
	if d == prefs.DriverACP && !f.Profiles[profile].HasACP() {
		return "", "", fmt.Errorf("profile %s has no ACP server (no acp args in its profile); use --driver tmux", profile)
	}
	return d, src, nil
}

// ExplainDriver is the driver a spawn of profile gets with no --driver and
// the layer that decided it: --driver > [profile.<name>] > [defaults] (not
// for an opt-in profile, ADR-76) > the tool profile > tmux.
func (f *Fleet) ExplainDriver(profile string) prefs.Setting {
	d, src, err := f.driverSource("", profile)
	if err != nil {
		return prefs.Setting{Key: "driver", Value: "refused: " + err.Error(), Source: src}
	}
	return prefs.Setting{Key: "driver", Value: d, Source: src}
}

// boardDriver is the board's Driver value for a driver name.
func boardDriver(d string) string {
	if d == prefs.DriverACP {
		return board.DriverACP
	}
	return ""
}

// isACP reports whether the agent on the board runs under the acp driver.
func (f *Fleet) isACP(name string) bool { return f.Store.Load().Agents[name].ACP() }

// ACPPromptPath is where spawn leaves an acp agent's first prompt for its
// host (--prompt-file): too long for a command line, and kept out of logs.
func ACPPromptPath(home, name string) string { return acphost.PromptPath(home, name) }

// ACPHostOpts are the acp-host options beyond its name and dir.
type ACPHostOpts struct {
	Session       string // resume or load this session
	PromptFile    string
	CopilotEvents bool
	Mode          string   // ACP session mode (ADR-76)
	MCP           string   // peer MCP config for the session (ADR-76)
	Meta          string   // _meta for the session calls (ADR-99)
	UnsetEnv      []string // variables the tool must not inherit (ADR-76)
	Profile       string   // shown in the pane's header (ADR-85)
	Model         string   // the session's model, set over ACP (ADR-112)
}

// ACPHostArgv is the command an acp agent's window runs: the host, its
// options, then the tool's ACP argv after "--".
func ACPHostArgv(exe, name, dir string, o ACPHostOpts, tool []string) []string {
	argv := []string{exe, "acp-host", name, "--dir", dir}
	if o.Session != "" {
		argv = append(argv, "--session", o.Session)
	}
	if o.PromptFile != "" {
		argv = append(argv, "--prompt-file", o.PromptFile)
	}
	if o.CopilotEvents {
		argv = append(argv, "--copilot-events")
	}
	if o.Mode != "" {
		argv = append(argv, "--mode", o.Mode)
	}
	if o.MCP != "" {
		argv = append(argv, "--mcp", o.MCP)
	}
	if o.Meta != "" {
		argv = append(argv, "--meta", o.Meta)
	}
	if len(o.UnsetEnv) > 0 {
		argv = append(argv, "--unset-env", strings.Join(o.UnsetEnv, ","))
	}
	if o.Profile != "" {
		argv = append(argv, "--profile", o.Profile)
	}
	if o.Model != "" {
		argv = append(argv, "--model", o.Model)
	}
	return append(append(argv, "--"), tool...)
}

// acpSetup is what an acp agent's host takes from its profile beyond the
// tool argv: the profile with its acp env in Env (start puts Env in the
// window), and the host options. For claude (ADR-76) that is the installed
// claude in CLAUDE_CODE_EXECUTABLE, the session mode, peer MCP over the
// session and the inherited secrets to drop.
func (f *Fleet) acpSetup(name, profile string, p tools.Profile, mode string, v tools.Vars) (tools.Profile, ACPHostOpts, error) {
	o := ACPHostOpts{CopilotEvents: copilotEvents(p), UnsetEnv: p.ACPUnsetEnv, Profile: profile, Meta: p.SessionMeta(v)}
	if len(p.ACPEnvVars) > 0 {
		path := ""
		if p.ACPNeedsCommandPath() {
			var err error
			if path, err = commandPath(p.Command); err != nil {
				return p, o, fmt.Errorf("profile %s runs %s under the acp driver, but it is not on PATH: %w", profile, p.Command, err)
			}
		}
		p.Env = p.ACPEnv(path)
	}
	o.Mode = p.ACPSessionMode(mode)
	if p.ACPSessionMCP() {
		_, o.MCP = tools.PeerMCP(f.Exe, name)
	}
	return p, o, nil
}

// commandPath finds cmd as the agent's window would: ~/.local/bin first,
// then PATH (tools.Profile.EnvList).
func commandPath(cmd string) (string, error) {
	if runtime.GOOS == "windows" {
		// No exec bits there; LookPath tries the PATHEXT extensions (.exe, .cmd).
		return exec.LookPath(cmd)
	}
	if strings.Contains(cmd, "/") {
		if fi, err := os.Stat(cmd); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			return "", fmt.Errorf("%s is not an executable", cmd)
		}
		return cmd, nil
	}
	home, _ := os.UserHomeDir()
	for _, d := range append([]string{filepath.Join(home, ".local/bin")}, filepath.SplitList(os.Getenv("PATH"))...) {
		if d == "" {
			continue
		}
		c := filepath.Join(d, cmd)
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return c, nil
		}
	}
	return "", fmt.Errorf("%s not found", cmd)
}

// copilotEvents: the Copilot CLI sends its own session events once asked
// to, which tell a foreground turn from a subagent's.
func copilotEvents(p tools.Profile) bool { return filepath.Base(p.Command) == "copilot" }

// acpMode is the configured mode when the profile knows it under acp.
func acpMode(name, profile string, p tools.Profile, mode string) string {
	if p.HasACPMode(mode) {
		return mode
	}
	logx.For("fleet").Warn("unknown acp mode for profile; ignoring", "agent", name, "profile", profile, "mode", mode)
	return ""
}

// launchACP starts a new acp agent: the host with the tool's ACP argv and
// the first prompt in a file. No paste, mode keys or ready wait: the host
// sends the prompt over ACP once the session is up.
func (f *Fleet) launchACP(name, profile string, p tools.Profile, dir, goal, full string, claimed bool, lvl playbook.Level, ex startExtra) (*board.Agent, error) {
	pr := f.Prefs.For(profile)
	mode := acpMode(name, profile, p, pr.Mode)
	v := f.launchVars(name, "", p, pr, fenceDir(ex.Worktree, pr.FenceWrites), true)
	tool := p.ACPArgv(v, tools.Launch{
		Mode: mode, Context: contextKey(p, lvl, ex.Role.Leader), Extra: modelExtra(p, ex.Model, pr.ExtraArgs)})
	p, o, err := f.acpSetup(name, profile, p, mode, v)
	if err != nil {
		return nil, err
	}
	f.forgetACP(name)
	pf := ""
	if full != "" {
		pf = ACPPromptPath(f.Home, name)
		if err := os.MkdirAll(filepath.Dir(pf), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(pf, []byte(full), 0o600); err != nil {
			return nil, err
		}
	}
	ex.Driver = board.DriverACP
	o.PromptFile, o.Model = pf, ex.Model
	argv := ACPHostArgv(f.Exe, name, dir, o, tool)
	return f.start(name, profile, p, dir, goal, full, "", argv, claimed, ex)
}

// wakeACPArgv is the window command that resumes a parked acp agent: a new
// host on the session the transcript recorded. It returns the profile to
// start it with (its acp env, see acpSetup).
func (f *Fleet) wakeACPArgv(old *board.Agent, p tools.Profile, ctx string) ([]string, tools.Profile, error) {
	if old.SessionID == "" {
		return nil, p, fmt.Errorf("agent %q has no ACP session id recorded; it can't be resumed", old.Name)
	}
	pr := f.Prefs.For(old.Profile)
	mode := acpMode(old.Name, old.Profile, p, pr.Mode)
	v := f.launchVars(old.Name, "", p, pr, fenceDir(old.Worktree, pr.FenceWrites), true)
	tool := p.ACPArgv(v, tools.Launch{
		Mode: mode, Context: ctx, Extra: modelExtra(p, old.Model, pr.ExtraArgs)})
	p, o, err := f.acpSetup(old.Name, old.Profile, p, mode, v)
	if err != nil {
		return nil, p, err
	}
	// Prompts an earlier host queued but never sent wait in the prompt file.
	o.Session, o.PromptFile, o.Model = old.SessionID, ACPPromptPath(f.Home, old.Name), old.Model
	if _, err := os.Stat(o.PromptFile); err != nil {
		o.PromptFile = ""
	}
	return ACPHostArgv(f.Exe, old.Name, old.Dir, o, tool), p, nil
}

// forgetACP drops what an earlier agent of this name left behind, so a new
// one never shows its status, line or session.
func (f *Fleet) forgetACP(name string) {
	for _, p := range []string{acphost.TranscriptPath(f.Home, name), ACPPromptPath(f.Home, name)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			logx.For("fleet").Warn("acp: remove stale file", "agent", name, "path", p, "err", err)
		}
	}
	f.forgetHook(name)
}

// acpCall sends one control request to the agent's host.
func (f *Fleet) acpCall(name string, req acphost.Request) (acphost.Response, error) {
	r, err := acphost.Call(f.Home, name, req, ACPCallTimeout)
	if err != nil {
		return r, fmt.Errorf("%s: %w", name, err)
	}
	return r, nil
}

// acpStop asks the host to close the session and exit, before its window is
// killed. A host that is already gone is not an error.
// ACPStopWait bounds how long a stop waits for the host to exit: its
// session/close and child exit timeouts, plus a margin.
var ACPStopWait = 7 * time.Second

func (f *Fleet) acpStop(name string) {
	if _, err := f.acpCall(name, acphost.Request{Op: acphost.OpStop}); err != nil {
		if !errors.Is(err, acphost.ErrNoHost) {
			logx.For("fleet").Warn("acp stop", "agent", name, "err", err)
		}
		return
	}
	// Wait for the host to go, so its session/close completes before the
	// window is killed.
	for end := time.Now().Add(ACPStopWait); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if _, err := acphost.Call(f.Home, name, acphost.Request{Op: acphost.OpState}, 500*time.Millisecond); errors.Is(err, acphost.ErrNoHost) {
			return
		}
	}
	logx.For("fleet").Warn("acp stop: host still up; killing its window", "agent", name, "after", ACPStopWait)
}

// acpClosingFor is how long a close's mark keeps the reaper off an acp
// agent; a close that died midway stops shielding it after this.
const acpClosingFor = time.Minute

// markClosing sets or clears (zero at) the board's closing mark.
func (f *Fleet) markClosing(name string, at time.Time) {
	f.Store.Update(func(b *board.Board) error {
		if a, ok := b.Agents[name]; ok {
			a.Closing = at
		}
		return nil
	})
}

// acpSocketMax is the longest control socket path that fits sun_path on
// every platform fleet runs on (104 bytes on darwin, NUL included).
const acpSocketMax = 103

// acpSocketFits refuses an agent whose control socket path is too long to
// listen on, before anything is started.
func acpSocketFits(home, name string) error {
	if p := acphost.SocketPath(home, name); len(p) > acpSocketMax {
		return fmt.Errorf("acp socket path %s is %d bytes, over the %d a unix socket allows: use a shorter agent name or FLEET_HOME", p, len(p), acpSocketMax)
	}
	return nil
}

// acpSend sends text as a prompt (or steers the running turn).
func (f *Fleet) acpSend(name, text string) error {
	if _, err := f.Pane(name); err != nil {
		return err
	}
	logx.For("fleet").Info("send", "agent", name, "bytes", len(text), "driver", "acp")
	if _, err := f.acpCall(name, acphost.Request{Op: acphost.OpPrompt, Text: text}); err != nil {
		return err
	}
	f.notePrompt(name)
	return nil
}

// acpKeys maps the keys fleet_send allows onto control requests: text with
// Enter is a prompt, Escape cancels the turn, a digit answers the open
// question. There is no input box to type into without submitting.
func (f *Fleet) acpKeys(name, text string, keys []string) error {
	switch {
	case text != "" && len(keys) == 1 && keys[0] == "Enter":
		return f.acpSend(name, text)
	case text == "" && len(keys) == 1 && keys[0] == "Escape":
		if _, err := f.Pane(name); err != nil {
			return err
		}
		_, err := f.acpCall(name, acphost.Request{Op: acphost.OpCancel})
		return err
	case text == "" && len(keys) == 1 && len(keys[0]) == 1 && keys[0] >= "1" && keys[0] <= "9":
		_, err := f.Choose(name, int(keys[0][0]-'0'), "")
		return err
	}
	return fmt.Errorf("%s runs over ACP: send text alone (a prompt), keys=[Escape] to cancel, or answer with fleet_answer", name)
}

// acpQuestion is the host's open permission request, nil when none.
func (f *Fleet) acpQuestion(name string) (*acphost.Question, error) {
	if _, err := f.Pane(name); err != nil {
		return nil, err
	}
	r, err := f.acpCall(name, acphost.Request{Op: acphost.OpState})
	if err != nil {
		return nil, err
	}
	return r.Question, nil
}

// acpMenu shows an ACP question as a numbered menu, so fleet_answer,
// waiting_for and the web view need nothing new.
func acpMenu(q *acphost.Question) status.Menu {
	m := status.Menu{Question: q.Title, Selected: 1, Numbered: true}
	for i, o := range q.Options {
		m.Options = append(m.Options, fmt.Sprintf("%d. %s", i+1, status.Cut(o.Name, status.LabelMax)))
	}
	return m
}

// acpChoose answers option (1-based) by its id, read fresh, so a question
// that changed since the menu was shown is checked against expect again.
func (f *Fleet) acpChoose(name string, option int, expect, gone string) (Choice, error) {
	q, err := f.acpQuestion(name)
	if err != nil {
		return Choice{}, err
	}
	if q == nil {
		return Choice{}, fmt.Errorf("%s", gone)
	}
	return f.acpAnswerIn(name, q, option, expect)
}

// acpAnswerIn answers option (1-based) of q, the question just read, by its
// option id, so the answer can't land on a different question's option.
func (f *Fleet) acpAnswerIn(name string, q *acphost.Question, option int, expect string) (Choice, error) {
	m := acpMenu(q)
	if option < 1 || option > len(m.Options) {
		return Choice{}, fmt.Errorf("option %d not offered (have 1-%d)", option, len(m.Options))
	}
	c := Choice{Label: m.Options[option-1], Question: m.Question}
	if !expected(expect, c.Question, status.Label(c.Label)) {
		return Choice{}, fmt.Errorf("prompt changed: now '%s'", c.Question)
	}
	if _, err := f.acpCall(name, acphost.Request{Op: acphost.OpAnswer, OptionID: q.Options[option-1].ID}); err != nil {
		return Choice{}, err
	}
	logx.For("fleet").Info("chose", "agent", name, "option", c.Label, "question", c.Question, "driver", "acp")
	return c, nil
}

// acpAnswer answers y or n by the options' ACP kinds: y the first allow,
// n the first reject.
func (f *Fleet) acpAnswer(name, yn, expect string) (Choice, error) {
	q, err := f.acpQuestion(name)
	if err != nil {
		return Choice{}, err
	}
	if q == nil {
		return Choice{}, fmt.Errorf("no open question")
	}
	want := "allow"
	if yn == "n" {
		want = "reject"
	}
	for i, o := range q.Options {
		if strings.HasPrefix(o.Kind, want) {
			return f.acpAnswerIn(name, q, i+1, expect)
		}
	}
	return Choice{}, fmt.Errorf("no %s option in %s; use option", want, strings.Join(acpMenu(q).Options, " | "))
}

// classifyACP is classify for an acp agent: the host's state file and
// transcript, never the screen.
// parkSettle is how long after ACPStopWait a park is still taking: the
// pane kill and its process-tree grace follow the wait.
const parkSettle = 5 * time.Second

func (f *Fleet) classifyACP(a *board.Agent, dead bool, pane string, now time.Time) string {
	// Park flips the entry to parked with no pane, then waits for the host
	// before it kills the pane. Reading the stopping host's state now would
	// undo the park and put the pane back, and the agent would go gone.
	if a.Status == string(status.Parked) && a.PaneID == "" && now.Sub(a.StatusSince) < ACPStopWait+parkSettle {
		return a.Status
	}
	a.PaneID = pane
	tr := readTranscript(acphost.TranscriptPath(f.Home, a.Name), a.SessionID == "")
	if tr.SessionID != "" {
		a.SessionID = tr.SessionID
	}
	// A prompt, answer or cancel typed in the pane is the user's key press,
	// whichever tmux client sent it (ADR-82).
	if tr.UserAt.After(a.UserAt) && tr.UserAt.After(a.Created) {
		a.UserAt = tr.UserAt
	}
	if dead {
		a.Stuck = nil
		a.LastLine = "process exited"
		if tr.Error != "" {
			a.LastLine = status.Cut(tr.Error, 200)
		}
		return string(status.Error)
	}
	if tr.Line != "" {
		a.LastLine = tr.Line
	}
	s, ok := status.ReadState(status.StateDir(f.Home), a.Name)
	h := fnv.New64a()
	fmt.Fprint(h, tr.Size, s.At.UnixNano(), s.Status)
	sum := h.Sum64()
	changed := sum != a.Hash
	if changed {
		a.Hash, a.LastChange = sum, now
	}
	st := status.Status(a.Status)
	switch {
	case ok:
		st = s.Status
		if s.WaitingFor != "" {
			a.LastLine = status.Cut(s.WaitingFor, 200)
		}
	case st == "" || st == status.Gone || st == status.Parked:
		st = status.Unknown
	}
	errLine := ""
	if st == status.Error {
		errLine = a.LastLine
	}
	st, a.Stuck = status.NextStuck(a.Stuck, status.StuckIn{
		Status:  st,
		Line:    errLine,
		Changed: changed,
		Still:   now.Sub(a.LastChange),
		After:   f.Prefs.For(a.Profile).StuckAfter,
		Now:     now,
	})
	return string(st)
}

// transcriptTail is how much of a transcript Refresh reads each poll.
const transcriptTail = 64 << 10

// transcript is what Refresh takes from an acp agent's transcript.
type transcript struct {
	Size      int64
	SessionID string    // the latest session entry
	Line      string    // the last line worth a tile
	Error     string    // the last error entry
	Lines     []string  // the tail, rendered as plain lines
	UserAt    time.Time // the newest entry the user typed in the pane (ADR-82)
}

// readTranscript reads the transcript's tail. full reads the whole file,
// so a session id written long ago is still found.
func readTranscript(path string, full bool) transcript {
	var t transcript
	fh, err := os.Open(path)
	if err != nil {
		return t
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return t
	}
	t.Size = st.Size()
	off := int64(0)
	if !full && t.Size > transcriptTail {
		off = t.Size - transcriptTail
	}
	b, err := io.ReadAll(io.NewSectionReader(fh, off, t.Size-off))
	if err != nil {
		return t
	}
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var e acphost.Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Kind {
		case acphost.KindSession:
			if e.SessionID != "" {
				t.SessionID = e.SessionID
			}
		case acphost.KindError:
			t.Error = e.Text
		}
		if e.By == acphost.ByPane && e.At.After(t.UserAt) {
			t.UserAt = e.At
		}
		t.Lines = append(t.Lines, renderEntry(e)...)
	}
	for i := len(t.Lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(t.Lines[i]); l != "" {
			t.Line = status.Cut(l, 200)
			break
		}
	}
	if len(t.Lines) > 200 {
		t.Lines = t.Lines[len(t.Lines)-200:]
	}
	return t
}

// renderEntry is an entry as the plain lines a tile or a crash report
// shows. Status changes, thoughts and session starts show nothing.
func renderEntry(e acphost.Entry) []string {
	switch e.Kind {
	case acphost.KindUser:
		return []string{"> " + firstLine(e.Text)}
	case acphost.KindAgent:
		return strings.Split(strings.TrimRight(e.Text, "\n"), "\n")
	case acphost.KindTool:
		if e.Status != "" {
			return []string{"• " + e.Title + " [" + e.Status + "]"}
		}
		return []string{"• " + e.Title}
	case acphost.KindPermission:
		var opts []string
		for _, o := range e.Options {
			opts = append(opts, o.Name)
		}
		return []string{"? " + e.Title + ": " + strings.Join(opts, " / ")}
	case acphost.KindAnswer:
		return []string{"→ " + e.Text}
	case acphost.KindError:
		return []string{"error: " + e.Text}
	}
	return nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// acpScreen is the transcript's last n lines (n <= 0: its tail), in place
// of a pane capture.
func (f *Fleet) acpScreen(name string, n int) string {
	ls := readTranscript(acphost.TranscriptPath(f.Home, name), false).Lines
	if n > 0 && len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}
