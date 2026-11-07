package fleet

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sessions"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// AdoptOpts are the inputs of Adopt. Name and Dir may be empty.
type AdoptOpts struct {
	Profile, SessionID, Dir, Name string
	Force                         bool // skip the session-exists and in-use checks
	// Admiral owns the adopted agent ("" = main, DESIGN §6.12).
	Admiral string
}

var (
	sessionIDRe = sessions.IDRe
	nameBadRe   = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
)

// Adopt resumes an existing session (started outside fleet) in a new agent
// window, like Spawn but with the profile's resume args and no prompt.
func (f *Fleet) Adopt(o AdoptOpts) (*board.Agent, error) {
	id := o.SessionID
	if !sessionIDRe.MatchString(id) {
		return nil, fmt.Errorf("bad session id %q", id)
	}
	p, ok := f.Profiles[o.Profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q (have %v)", o.Profile, tools.Names(f.Profiles))
	}
	if len(p.Resume) == 0 {
		return nil, fmt.Errorf("profile %s can't resume", o.Profile)
	}
	matches := p.SessionMatches(id)
	if !o.Force {
		if p.SessionPath != "" && len(matches) == 0 {
			return nil, fmt.Errorf("no %s session %s (looked in %s)", o.Profile, id, p.SessionGlob(id))
		}
		if err := inUse(id, matches); err != nil {
			return nil, err
		}
	}
	dir, err := adoptDir(o.Dir, matches)
	if err != nil {
		return nil, err
	}
	name, err := f.adoptName(o.Name, dir)
	if err != nil {
		return nil, err
	}
	// Resuming in the home directory is the same mistake as spawning there
	// (ADR-47): the session keeps its history, it just gets a dir of its own.
	dir, _ = f.safeDir(name, dir)
	f.TrustDir(o.Profile, p, dir)
	// An adopted session is a fresh process, so it needs the permission mode
	// a spawn would have given it (ADR-56).
	apr := f.Prefs.For(o.Profile)
	argv, _ := p.Argv(tools.AgentVars(f.Exe, name, id),
		tools.Launch{Resume: true, Mode: f.modeFor(name, o.Profile, p, apr.Mode), Extra: apr.ExtraArgs})
	// Adopt resumes a session that already has a dir, so it never makes a
	// worktree; when that dir is one, the board records it so close and
	// sweep can name the work left there (DESIGN §4, ADR-39).
	a, err := f.start(name, o.Profile, p, dir, "", "", id, argv, false,
		startExtra{Worktree: adoptWorktree(dir), NoWorktree: true, Role: Role{Admiral: o.Admiral}})
	if err != nil {
		return nil, err
	}
	logx.For("fleet").Info("adopted", "agent", name, "profile", o.Profile, "session_id", id, "force", o.Force)
	return a, nil
}

// adoptWorktree records the linked worktree an adopted dir is already in.
func adoptWorktree(dir string) *board.Worktree {
	w := worktree.Of(dir)
	if w == nil {
		return nil
	}
	return &board.Worktree{Path: w.Path, Branch: w.Branch, Repo: w.Repo}
}

// adoptDir picks the working dir: given > workspace.yaml cwd > current dir.
func adoptDir(dir string, matches []string) (string, error) {
	if dir == "" {
		for _, m := range matches {
			if d := workspaceCwd(m); d != "" {
				dir = d
				break
			}
		}
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return "", fmt.Errorf("dir %s is not a directory", abs)
	}
	return abs, nil
}

// workspaceCwd reads the `cwd:` line of a Copilot session folder's workspace.yaml.
func workspaceCwd(sessionDir string) string {
	fh, err := os.Open(filepath.Join(sessionDir, "workspace.yaml"))
	if err != nil {
		return ""
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "cwd:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// adoptName returns an explicit free name, or a safe base(dir) with -2, -3… if taken.
func (f *Fleet) adoptName(name, dir string) (string, error) {
	taken := func(n string) bool {
		_, onBoard := f.Store.Load().Agents[n]
		return onBoard || f.paneOf(n) != ""
	}
	if name != "" {
		if !validName(name) {
			return "", fmt.Errorf("bad name %q", name)
		}
		if taken(name) {
			return "", fmt.Errorf("agent %q already exists", name)
		}
		return name, nil
	}
	base := strings.Trim(nameBadRe.ReplaceAllString(filepath.Base(dir), "-"), "-_.")
	if base == "" || !validName(base[:min(len(base), 32)]) {
		base = "adopted"
	}
	for i := 1; ; i++ {
		suffix := ""
		if i > 1 {
			suffix = "-" + strconv.Itoa(i)
		}
		n := base[:min(len(base), 32-len(suffix))] + suffix
		if !taken(n) {
			return n, nil
		}
	}
}

// inUse refuses when a live process holds the session: a Copilot
// inuse.<pid>.lock in the session folder, or a command line with the id.
func inUse(id string, matches []string) error {
	if pid, cmd := sessions.Holder(id, matches, sessions.Procs()); pid != 0 {
		return inUseErr(id, pid, cmd)
	}
	return nil
}

func inUseErr(id string, pid int, cmd string) error {
	if len(cmd) > 60 {
		cmd = cmd[:60] + "…"
	}
	return fmt.Errorf("session %s is in use by pid %d (%s); close that terminal first or use --force", id, pid, cmd)
}
