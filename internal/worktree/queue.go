package worktree

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// QueueItem is one branch in a merge queue (ADR-206), with how many files it
// changes against main: fewer goes first, as in a single merge's plan.
type QueueItem struct {
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Files  int    `json:"files"`
}

// Candidate is a local fleet/ branch the queue may take: Pushed when its
// remote-tracking ref is at the same commit, Merged when main has its work.
type Candidate struct {
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Pushed bool   `json:"pushed"`
	Merged bool   `json:"merged"`
}

// Candidates lists the repo's local fleet/ branches for `fleet merge --queue`.
func Candidates(dir string) ([]Candidate, error) {
	out, err := git(dir, "for-each-ref", "--format=%(refname:short) %(objectname) %(upstream)", "refs/heads/"+BranchPrefix)
	if err != nil {
		return nil, err
	}
	var cs []Candidate
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		c := Candidate{Branch: f[0], Head: f[1]}
		if len(f) == 3 {
			up, err := revParse(dir, f[2])
			c.Pushed = err == nil && up == c.Head
		}
		cs = append(cs, c)
	}
	heads := make([]string, len(cs))
	for i, c := range cs {
		heads[i] = c.Head
	}
	for i, m := range MergedAll(dir, heads) {
		cs[i].Merged = m
	}
	return cs, nil
}

// OrderQueue reads each branch's head and changed-file count against the base
// and sorts them fewer files first, branch name breaking a tie.
func OrderQueue(dir string, branches []string) ([]QueueItem, error) {
	base := baseBranch(dir)
	if base == "" {
		return nil, fmt.Errorf("no main or master branch to merge into")
	}
	items := make([]QueueItem, 0, len(branches))
	for _, b := range branches {
		head, err := revParse(dir, b)
		if err != nil {
			return nil, fmt.Errorf("branch %s: %w", b, err)
		}
		out, err := git(dir, "diff", "--name-only", base+"..."+head)
		if err != nil {
			return nil, fmt.Errorf("branch %s: %w", b, err)
		}
		items = append(items, QueueItem{Branch: b, Head: head, Files: len(strings.Fields(out))})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Files != items[j].Files {
			return items[i].Files < items[j].Files
		}
		return items[i].Branch < items[j].Branch
	})
	return items, nil
}

// QueueOptions configures Queue.
type QueueOptions struct {
	Dir        string                 // any directory of the repo
	Items      []QueueItem            // in merge order (OrderQueue)
	Check      func(dir string) error // run once per batch tip; nil skips
	AppendOnly map[string]bool        // as Options.AppendOnly
	Log        func(string)
	Ignore     []string
}

// Ejected is a queued branch that did not land, and why: a conflict outside
// the append-only files, or the check failing on it (the error carries its
// output).
type Ejected struct {
	Branch string `json:"branch"`
	Reason string `json:"reason"`
}

// QueueResult is what a queue run did to the base branch.
type QueueResult struct {
	Base    string    `json:"base"`
	BaseDir string    `json:"base_dir"`
	Tip     string    `json:"tip"`
	Merged  []string  `json:"merged"`
	Ejected []Ejected `json:"ejected"`
	Notes   []string  `json:"notes,omitempty"` // branches whose ref fleet could not move to the landed commits
}

type queue struct {
	QueueOptions
	scratch string
	logf    func(string)
	res     *QueueResult
}

type landed struct {
	item QueueItem
	sha  string // the rebased head in the batch
}

// Queue lands a batch of branches on the base branch in one check, Bors
// style (ADR-206): it rebases them one after another onto the base in a
// scratch worktree with the same rebase a single merge uses, runs the check
// once on the tip and fast-forwards the base checkout to it. A red tip is
// bisected: each half is rebuilt and checked on the good part below it, down
// to the branch that breaks it. A conflict outside the append-only files
// ejects that branch. Each landed branch's ref moves to its rebased commits,
// as a single merge leaves it. It never pushes and never touches a branch
// that was not merged.
func Queue(o QueueOptions) (*QueueResult, error) {
	top, err := TopLevel(o.Dir)
	if err != nil {
		return nil, err
	}
	base := baseBranch(top)
	if base == "" {
		return nil, fmt.Errorf("no main or master branch to merge into")
	}
	baseDir, err := branchWorktree(top, base)
	if err != nil {
		return nil, err
	}
	if dirty, err := dirtyFiles(baseDir); err != nil {
		return nil, err
	} else if len(dirty) > 0 {
		return nil, fmt.Errorf("%s has uncommitted changes: %s", baseDir, strings.Join(dirty, ", "))
	}
	start, err := revParse(baseDir, base)
	if err != nil {
		return nil, err
	}
	q := &queue{QueueOptions: o, logf: o.Log, res: &QueueResult{Base: base, BaseDir: baseDir, Tip: start}}
	if q.logf == nil {
		q.logf = func(string) {}
	}
	if q.scratch, err = os.MkdirTemp("", "fleet-queue-"); err != nil {
		return nil, fmt.Errorf("scratch worktree: %w", err)
	}
	defer q.cleanup(top)
	if _, err := git(top, "worktree", "add", "--quiet", "--detach", q.scratch, start); err != nil {
		return nil, fmt.Errorf("scratch worktree: %w", err)
	}
	tip, ok, err := q.land(o.Items, start)
	if err != nil {
		return q.res, err
	}
	if tip == start {
		return q.res, nil
	}
	q.logf(fmt.Sprintf("fast-forwarding %s to %s", base, shortSHA(tip)))
	if _, err := git(baseDir, "merge", "--ff-only", "--quiet", tip); err != nil {
		return q.res, fmt.Errorf("%s moved during the queue run; nothing merged, run it again: %w", base, err)
	}
	q.res.Tip = tip
	for _, l := range ok {
		q.res.Merged = append(q.res.Merged, l.item.Branch)
		q.moveBranch(top, l)
	}
	return q.res, nil
}

// land builds items on onto and checks the tip; on red it splits the batch
// in two and lands each half on the good part below it. It returns the new
// tip and the branches in it.
func (q *queue) land(items []QueueItem, onto string) (string, []landed, error) {
	tip, built, err := q.build(items, onto)
	if err != nil || len(built) == 0 {
		return onto, nil, err
	}
	batch := make([]QueueItem, len(built))
	names := make([]string, len(built))
	for i, l := range built {
		batch[i] = l.item
		names[i] = l.item.Branch
	}
	q.logf(fmt.Sprintf("checking %s on %s", strings.Join(names, ", "), shortSHA(tip)))
	cerr := q.check(tip)
	if cerr == nil {
		return tip, built, nil
	}
	if len(built) == 1 {
		q.logf(fmt.Sprintf("%s fails the check; ejected", names[0]))
		q.eject(built[0].item.Branch, cerr.Error())
		return onto, nil, nil
	}
	q.logf(fmt.Sprintf("batch of %d fails the check; bisecting", len(built)))
	mid := len(batch) / 2
	t1, ok1, err := q.land(batch[:mid], onto)
	if err != nil {
		return onto, nil, err
	}
	t2, ok2, err := q.land(batch[mid:], t1)
	if err != nil {
		return onto, nil, err
	}
	return t2, append(ok1, ok2...), nil
}

// build rebases each item onto the tip so far, in the scratch worktree.
func (q *queue) build(items []QueueItem, onto string) (string, []landed, error) {
	tip := onto
	var built []landed
	for _, it := range items {
		if err := q.reset(it.Head); err != nil {
			return onto, nil, err
		}
		// The Plan carries the real branch name so the replayed commits get
		// their Fleet-Agent trailer, as in a single merge.
		p := &Plan{Dir: q.scratch, Branch: it.Branch, Base: tip, ignore: q.Ignore}
		if err := rebase(p, Options{AppendOnly: q.AppendOnly}.appendOnly(), q.logf); err != nil {
			q.logf(fmt.Sprintf("%s does not rebase; ejected", it.Branch))
			q.eject(it.Branch, err.Error())
			continue
		}
		head, err := revParse(q.scratch, "HEAD")
		if err != nil {
			return onto, nil, err
		}
		tip = head
		built = append(built, landed{item: it, sha: head})
	}
	return tip, built, nil
}

func (q *queue) check(tip string) error {
	if q.Check == nil {
		return nil
	}
	if err := q.reset(tip); err != nil {
		return err
	}
	return q.Check(q.scratch)
}

// reset puts the scratch worktree on rev, dropping whatever a check changed.
func (q *queue) reset(rev string) error {
	if _, err := git(q.scratch, "reset", "--hard", "--quiet"); err != nil {
		return err
	}
	_, err := git(q.scratch, "checkout", "--quiet", "--detach", rev)
	return err
}

func (q *queue) eject(branch, reason string) {
	q.res.Ejected = append(q.res.Ejected, Ejected{Branch: branch, Reason: reason})
}

// moveBranch points a landed branch at its rebased commits, as `fleet merge`
// leaves it, so its agent sees it merged. A branch that moved since it was
// queued keeps its new commits; a checked-out one moves only with
// `reset --keep`, which refuses to lose its worktree's changes.
func (q *queue) moveBranch(top string, l landed) {
	note := func(why string) {
		q.res.Notes = append(q.res.Notes, l.item.Branch+": "+why)
	}
	if now, err := revParse(top, l.item.Branch); err != nil || now != l.item.Head {
		note("moved since it was queued; its newer commits are not merged")
		return
	}
	wt, err := branchWorktree(top, l.item.Branch)
	if err != nil {
		if _, err := git(top, "branch", "--force", l.item.Branch, l.sha); err != nil {
			note("branch not moved to the landed commits: " + err.Error())
		}
		return
	}
	if _, err := git(wt, "reset", "--quiet", "--keep", l.sha); err != nil {
		note("worktree " + wt + " not moved to the landed commits: " + err.Error())
	}
}

func (q *queue) cleanup(top string) {
	if _, err := git(top, "worktree", "remove", "--force", q.scratch); err != nil {
		q.logf("scratch worktree left at " + q.scratch + ": " + err.Error())
		return
	}
	_ = os.RemoveAll(q.scratch) // already gone after worktree remove; belt and braces
}
