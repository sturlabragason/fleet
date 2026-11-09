package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// SharedDocs are the two repo-root files every parallel agent appends to, so a
// conflict in them is mechanical: keep both sides (docs/process-review.md
// finding 3, ADR-48). The value says where the replayed commit's lines go.
// CHANGELOG.md is newest-first at the top of today's section, so our own line
// goes above the base's; DESIGN.md §14 appends ADRs in ascending order at the
// bottom, so the base's entry stays above ours. A conflict in anything else is
// a real overlap and stops the merge.
var SharedDocs = map[string]bool{
	"CHANGELOG.md": true,  // replayed side first (newest at the top)
	"DESIGN.md":    false, // base side first (ADRs ascend)
}

// sharedDoc reports whether f is one of them, and whether the replayed side
// goes above the base's.
func sharedDoc(f string) (theirsFirst, ok bool) {
	theirsFirst, ok = SharedDocs[path.Clean(f)]
	return theirsFirst, ok
}

// Plan is the order `fleet merge` would take: this worktree's branch, the base
// it rebases onto, the checkout that holds the base, and every other worktree
// whose files overlap ours, each marked with whether it merges before us.
type Plan struct {
	Dir        string      `json:"dir"`
	Branch     string      `json:"branch"`
	Base       string      `json:"base"`
	BaseDir    string      `json:"base_dir"`
	Ahead      int         `json:"ahead"`
	Behind     int         `json:"behind"`
	Files      []string    `json:"files"`
	Neighbours []Neighbour `json:"neighbours"`
	ignore     []string
}

// Neighbour is another worktree that changes some of the same files. Files is
// the overlap, not everything it changes.
type Neighbour struct {
	Path   string   `json:"path"`
	Branch string   `json:"branch"`
	Files  []string `json:"files"`
	First  bool     `json:"first"` // it merges before us
	Why    string   `json:"why"`
}

// NewPlan reads the branch state `fleet merge` acts on. It fails when dir is
// not a feature worktree with its own branch and a base to merge into.
// ignore (absolute paths, e.g. agents' result files) is left out of every
// worktree's uncommitted changes, as in Changes.
func NewPlan(dir string, ignore ...string) (*Plan, error) {
	top, err := TopLevel(dir)
	if err != nil {
		return nil, err
	}
	branch, err := currentBranch(top)
	if err != nil {
		return nil, err
	}
	base := baseBranch(top)
	if base == "" {
		return nil, fmt.Errorf("no main or master branch to merge into")
	}
	if branch == base {
		return nil, fmt.Errorf("already on %s: run fleet merge from the feature worktree", base)
	}
	baseDir, err := branchWorktree(top, base)
	if err != nil {
		return nil, err
	}
	p := &Plan{Dir: top, Branch: branch, Base: base, BaseDir: baseDir, ignore: ignore}
	if p.Files, err = Changes(top, ignore...); err != nil {
		return nil, err
	}
	if p.Ahead, p.Behind, err = aheadBehind(top, base); err != nil {
		return nil, err
	}
	others, err := Others(top, ignore...)
	if err != nil {
		return nil, err
	}
	head, err := revParse(top, "HEAD")
	if err != nil {
		return nil, err
	}
	total := map[string]int{} // every file a worktree changes, not just the overlap
	for _, w := range others {
		total[w.Path] = len(w.Files)
	}
	for _, w := range Overlap(others, p.Files) {
		o := Neighbour{Path: w.Path, Branch: w.Branch, Files: w.Files}
		switch {
		case p.Ahead > 0 && contains(w.Path, head):
			o.First, o.Why = true, "its branch already contains ours"
		case total[w.Path] != len(p.Files):
			o.First = total[w.Path] < len(p.Files)
			o.Why = "fewer touched files merge first (cheaper rebase)"
		default:
			o.First = w.Branch < p.Branch
			o.Why = "same size: branch name breaks the tie"
		}
		p.Neighbours = append(p.Neighbours, o)
	}
	sort.Slice(p.Neighbours, func(i, j int) bool { return p.Neighbours[i].Path < p.Neighbours[j].Path })
	return p, nil
}

// Steps renders the plan as the ordered lines --dry-run prints.
func (p *Plan) Steps() []string {
	var s []string
	for _, o := range p.Neighbours {
		if o.First {
			s = append(s, fmt.Sprintf("wait for %s (%s): %s — overlaps %s",
				o.Branch, o.Path, o.Why, strings.Join(o.Files, ", ")))
		}
	}
	for _, o := range p.Neighbours {
		if !o.First {
			s = append(s, fmt.Sprintf("go before %s (%s): %s", o.Branch, o.Path, o.Why))
		}
	}
	s = append(s,
		fmt.Sprintf("rebase %s onto %s (%d ahead, %d behind)", p.Branch, p.Base, p.Ahead, p.Behind),
		"run the checks on the rebased branch",
		fmt.Sprintf("merge %s into %s in %s (fast-forward when possible, else --no-ff)", p.Branch, p.Base, p.BaseDir),
		fmt.Sprintf("run the checks again on %s", p.Base))
	return s
}

// ConflictError is a rebase conflict outside the two shared docs: a real
// overlap with another agent's work.
type ConflictError struct {
	Files     []string
	Worktrees []string
}

func (e *ConflictError) Error() string {
	m := fmt.Sprintf("conflict in %s", strings.Join(e.Files, ", "))
	if len(e.Worktrees) > 0 {
		m += "; also changed by " + strings.Join(e.Worktrees, ", ")
	}
	return m + " (rebase aborted)"
}

// Options configures Merge.
type Options struct {
	Dir     string                 // any directory inside the feature worktree
	Check   func(dir string) error // build/vet/test; nil skips
	Retries int                    // rebase-and-merge attempts when the base moves (default 3)
	Log     func(string)           // progress lines
	Ignore  []string               // absolute paths left out of the overlap, see NewPlan
}

// Merge rebases the current branch onto the base branch, runs the checks, and
// merges into the base checkout, rerunning the checks there (ADR-48). It
// resolves conflicts in CHANGELOG.md and DESIGN.md by keeping both sides,
// newest first; any other conflict aborts the rebase and returns a
// *ConflictError. If the base moves while the checks run it starts over, up to
// Retries times. It never pushes and never removes the worktree.
func Merge(o Options) (*Plan, error) {
	p, err := NewPlan(o.Dir, o.Ignore...)
	if err != nil {
		return nil, err
	}
	logf := o.Log
	if logf == nil {
		logf = func(string) {}
	}
	if dirty, err := dirtyFiles(p.Dir); err != nil {
		return p, err
	} else if len(dirty) > 0 {
		return p, fmt.Errorf("%s has uncommitted changes: %s", p.Dir, strings.Join(dirty, ", "))
	}
	if dirty, err := dirtyFiles(p.BaseDir); err != nil {
		return p, err
	} else if len(dirty) > 0 {
		return p, fmt.Errorf("%s has uncommitted changes: %s", p.BaseDir, strings.Join(dirty, ", "))
	}
	tries := o.Retries
	if tries <= 0 {
		tries = 3
	}
	for attempt := 1; ; attempt++ {
		logf(fmt.Sprintf("rebasing %s onto %s", p.Branch, p.Base))
		if err := rebase(p, logf); err != nil {
			return p, err
		}
		// The commit the rebase landed on, not the one read before it: the
		// base can move in that window without making this rebase stale.
		before, err := mergeBase(p.Dir, "HEAD", p.Base)
		if err != nil {
			return p, err
		}
		if o.Check != nil {
			logf("checks on " + p.Branch)
			if err := o.Check(p.Dir); err != nil {
				return p, fmt.Errorf("checks failed on %s: %w", p.Branch, err)
			}
		}
		moved, err := revParse(p.BaseDir, p.Base)
		if err != nil {
			return p, err
		}
		if moved != before {
			if attempt >= tries {
				return p, fmt.Errorf("%s moved during each of %d attempts; try again when the fleet is quieter", p.Base, tries)
			}
			logf(fmt.Sprintf("%s moved while the checks ran; retrying (%d/%d)", p.Base, attempt+1, tries))
			continue
		}
		logf(fmt.Sprintf("merging %s into %s", p.Branch, p.Base))
		if _, err := git(p.BaseDir, "merge", "--ff-only", p.Branch); err != nil {
			if now, e := revParse(p.BaseDir, p.Base); e == nil && now != before {
				if attempt >= tries {
					return p, fmt.Errorf("%s moved during each of %d attempts; try again when the fleet is quieter", p.Base, tries)
				}
				logf(fmt.Sprintf("%s moved before the merge; retrying (%d/%d)", p.Base, attempt+1, tries))
				continue
			}
			if _, err := gitEnv(p.BaseDir, []string{"GIT_EDITOR=true"}, "merge", "--no-ff", "--no-edit", p.Branch); err != nil {
				// Leave no half-merge behind: a MERGING base checkout
				// blocks every other agent's fleet merge.
				if _, e := git(p.BaseDir, "merge", "--abort"); e != nil {
					logx.For("worktree").Warn("merge --abort failed", "dir", p.BaseDir, "err", e)
				}
				return p, fmt.Errorf("merging %s into %s failed, merge aborted: %w", p.Branch, p.Base, err)
			}
		}
		if o.Check != nil {
			logf("checks on " + p.Base)
			if err := o.Check(p.BaseDir); err != nil {
				head, _ := revParse(p.BaseDir, p.Base)
				return p, fmt.Errorf("checks failed on %s after the merge: %w; %s now contains the merge at %s — fix forward, fleet does not reset your %s (ADR-29)",
					p.Base, err, p.Base, shortSHA(head), p.Base)
			}
		}
		return p, nil
	}
}

// rebase replays the branch onto the base, resolving shared-doc conflicts as
// they come and aborting on any other.
func rebase(p *Plan, logf func(string)) error {
	_, err := git(p.Dir, "rebase", p.Base)
	for i := 0; err != nil; i++ {
		if i > 100 {
			abortRebase(p.Dir)
			return fmt.Errorf("rebase made no progress after 100 conflicts")
		}
		unmerged, uerr := unmergedFiles(p.Dir)
		if uerr != nil || len(unmerged) == 0 {
			abortRebase(p.Dir)
			return fmt.Errorf("rebase onto %s failed: %w", p.Base, err)
		}
		var bad []string
		for _, f := range unmerged {
			if _, ok := sharedDoc(f); !ok {
				bad = append(bad, f)
			}
		}
		if len(bad) > 0 {
			abortRebase(p.Dir)
			return &ConflictError{Files: bad, Worktrees: othersTouching(p, bad)}
		}
		for _, f := range unmerged {
			// Replaying our commit onto the base makes "theirs" our own
			// side; where it goes is the file's own rule (SharedDocs).
			theirsFirst, _ := sharedDoc(f)
			if err := resolveFile(p.Dir, f, theirsFirst); err != nil {
				abortRebase(p.Dir)
				return err
			}
			if _, err := git(p.Dir, "add", "--", f); err != nil {
				abortRebase(p.Dir)
				return err
			}
		}
		logf("kept both sides in " + strings.Join(unmerged, ", "))
		_, err = gitEnv(p.Dir, []string{"GIT_EDITOR=true"}, "rebase", "--continue")
	}
	return nil
}

// resolveFile rewrites one conflicted file in place, keeping both sides.
func resolveFile(dir, file string, theirsFirst bool) error {
	p := path.Join(dir, file)
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	out, ok := KeepBoth(string(b), theirsFirst)
	if !ok {
		return fmt.Errorf("%s: no conflict markers to resolve", file)
	}
	return os.WriteFile(p, []byte(out), 0o644)
}

// KeepBoth replaces every conflict region in content with both sides' lines,
// the newer side first and no markers left. With theirsFirst the incoming
// (replayed) side goes above. It reports whether any marker was found.
func KeepBoth(content string, theirsFirst bool) (string, bool) {
	lines := strings.Split(content, "\n")
	var out []string
	var ours, theirs []string
	const (
		plain = iota
		inOurs
		inBase
		inTheirs
	)
	state, found := plain, false
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "<<<<<<<") && state == plain:
			state, found = inOurs, true
			ours, theirs = nil, nil
		case strings.HasPrefix(l, "|||||||") && state == inOurs:
			state = inBase
		case strings.HasPrefix(l, "=======") && (state == inOurs || state == inBase):
			state = inTheirs
		case strings.HasPrefix(l, ">>>>>>>") && state == inTheirs:
			state = plain
			if theirsFirst {
				out = append(out, theirs...)
				out = append(out, ours...)
			} else {
				out = append(out, ours...)
				out = append(out, theirs...)
			}
		default:
			switch state {
			case inOurs:
				ours = append(ours, l)
			case inTheirs:
				theirs = append(theirs, l)
			case inBase: // the merge base, dropped
			default:
				out = append(out, l)
			}
		}
	}
	if state != plain {
		return content, false
	}
	return strings.Join(out, "\n"), found
}

// othersTouching names the other worktrees that change any of files.
func othersTouching(p *Plan, files []string) []string {
	ws, err := Others(p.Dir, p.ignore...)
	if err != nil {
		logx.For("worktree").Warn("other worktrees unreadable", "err", err)
		return nil
	}
	var names []string
	for _, w := range Overlap(ws, files) {
		names = append(names, w.Branch+" ("+w.Path+")")
	}
	sort.Strings(names)
	return names
}

func abortRebase(dir string) {
	if _, err := git(dir, "rebase", "--abort"); err != nil {
		logx.For("worktree").Warn("rebase --abort failed", "dir", dir, "err", err)
	}
}

func unmergedFiles(dir string) ([]string, error) {
	out, err := git(dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files, nil
}

func dirtyFiles(dir string) ([]string, error) {
	out, err := git(dir, "status", "--porcelain", "-z", "--untracked-files=no")
	if err != nil {
		return nil, err
	}
	return ParseStatus(out), nil
}

func currentBranch(dir string) (string, error) {
	out, err := git(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("%s is not on a branch (detached HEAD)", dir)
	}
	return strings.TrimSpace(out), nil
}

// branchWorktree finds the checkout that has branch checked out.
func branchWorktree(dir, branch string) (string, error) {
	out, err := git(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	for _, w := range Parse(out) {
		if !w.Bare && !w.Prunable && w.Branch == branch {
			return realPath(w.Path), nil
		}
	}
	return "", fmt.Errorf("no worktree has %s checked out", branch)
}

func mergeBase(dir, a, b string) (string, error) {
	out, err := git(dir, "merge-base", a, b)
	return strings.TrimSpace(out), err
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func revParse(dir, rev string) (string, error) {
	out, err := git(dir, "rev-parse", "--verify", rev)
	return strings.TrimSpace(out), err
}

func aheadBehind(dir, base string) (ahead, behind int, err error) {
	out, err := git(dir, "rev-list", "--left-right", "--count", "HEAD..."+base)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("rev-list gave %q", out)
	}
	ahead, _ = strconv.Atoi(f[0])
	behind, _ = strconv.Atoi(f[1])
	return ahead, behind, nil
}

// contains reports whether the worktree at path has commit sha in its history.
func contains(wt, sha string) bool {
	_, err := git(wt, "merge-base", "--is-ancestor", sha, "HEAD")
	return err == nil
}

// gitEnv is git with extra environment, for the commands that would otherwise
// open an editor.
func gitEnv(dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	for _, e := range os.Environ() {
		switch k, _, _ := strings.Cut(e, "="); k {
		case "GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE":
		default:
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
