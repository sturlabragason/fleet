package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/sysx"
	"github.com/sturlabragason/fleet/internal/update"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// queueTail is how many lines of a failed check's output a queue report keeps.
const queueTail = 40

// mergeQueue is `fleet merge --queue [branches...]` (ADR-206): land the named
// branches, or every ready one, in one checked batch on local main.
func mergeQueue(ctx context.Context, dir string, branches []string, check string, dry, asJSON bool, cfg worktree.RepoConfig) error {
	top, err := worktree.TopLevel(dir)
	if err != nil {
		return fail(err)
	}
	var skipped []string
	if len(branches) == 0 {
		if branches, skipped, err = readyBranches(ctx, top); err != nil {
			return fail(err)
		}
	}
	items, err := worktree.OrderQueue(top, branches)
	if err != nil {
		return fail(err)
	}
	if dry {
		return printQueuePlan(items, skipped, check, asJSON)
	}
	for _, s := range skipped {
		fmt.Fprintln(os.Stderr, "fleet merge: skipped "+s)
	}
	if len(items) == 0 {
		fmt.Println("fleet merge: no ready branches to queue")
		return nil
	}
	opts := worktree.QueueOptions{Dir: top, Items: items, AppendOnly: cfg.AppendOnly, Ignore: resultFiles(),
		Log: func(s string) { fmt.Fprintln(os.Stderr, "fleet merge:", s) }}
	if strings.TrimSpace(check) != "" {
		if err := checkRunnable(top, check); err != nil {
			return fail(err)
		}
		opts.Check = func(d string) error { return capturedCheck(ctx, d, check) }
	}
	res, err := worktree.Queue(opts)
	if res != nil {
		printQueueResult(res)
		for _, b := range res.Merged {
			recordMerge(b)
		}
	}
	if err != nil {
		return fail(err)
	}
	if len(res.Merged) > 0 {
		rebuildGraph(res.BaseDir)
	}
	if len(res.Ejected) > 0 {
		return exitCode(1)
	}
	return nil
}

// readyBranches are the local fleet/ branches not yet on main, pushed as they
// are, whose head commit has green CI; skipped says why each other one waits.
func readyBranches(ctx context.Context, top string) (ready, skipped []string, err error) {
	cs, err := worktree.Candidates(top)
	if err != nil {
		return nil, nil, err
	}
	gh, err := companion.Find(update.GH)
	if err != nil {
		return nil, nil, fmt.Errorf("CI status needs gh: %w; or name the branches to queue", err)
	}
	for _, c := range cs {
		if c.Merged {
			continue
		}
		why := "not pushed as it is (no upstream, or the local head differs)"
		if c.Pushed {
			why = ciVerdict(ctx, gh, top, c.Branch, c.Head)
		}
		if why != "" {
			skipped = append(skipped, c.Branch+": "+why)
			continue
		}
		ready = append(ready, c.Branch)
	}
	return ready, skipped, nil
}

// ciVerdict is "" when every CI run on head finished green, else why not.
func ciVerdict(ctx context.Context, gh companion.Tool, dir, branch, head string) string {
	var runs []struct {
		HeadSha    string `json:"headSha"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	err := gh.JSON(ctx, companion.Opts{Dir: dir, Timeout: 30 * time.Second}, &runs,
		"run", "list", "--branch", branch, "--limit", "20", "--json", "headSha,status,conclusion")
	if err != nil {
		return "CI status unreadable: " + err.Error()
	}
	seen := 0
	for _, r := range runs {
		if r.HeadSha != head {
			continue
		}
		seen++
		switch {
		case r.Status != "completed":
			return "CI still running on its head"
		case r.Conclusion != "success" && r.Conclusion != "skipped" && r.Conclusion != "neutral":
			return "CI " + r.Conclusion + " on its head"
		}
	}
	if seen == 0 {
		return "no CI run on its head"
	}
	return ""
}

func printQueuePlan(items []worktree.QueueItem, skipped []string, check string, asJSON bool) error {
	if asJSON {
		b, _ := json.MarshalIndent(struct {
			Queue   []worktree.QueueItem `json:"queue"`
			Skipped []string             `json:"skipped,omitempty"`
		}{items, skipped}, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	for i, it := range items {
		fmt.Printf("%d. %s (%d files)\n", i+1, it.Branch, it.Files)
	}
	for _, s := range skipped {
		fmt.Println("skip " + s)
	}
	if len(items) == 0 {
		fmt.Println("nothing to queue")
		return nil
	}
	if strings.TrimSpace(check) == "" {
		check = "no check"
	}
	fmt.Printf("rebase them in this order onto main in a scratch worktree (a conflict outside the append-only docs ejects that branch),\n"+
		"run %q once on the batch tip, and fast-forward local main to it.\n"+
		"On a red tip, split the batch in halves and check each on the good part below it until the failing branch is found;\n"+
		"merge the rest and report it with the check's output. Never pushes.\n", check)
	return nil
}

func printQueueResult(res *worktree.QueueResult) {
	for _, b := range res.Merged {
		fmt.Printf("merged %s\n", b)
	}
	for _, e := range res.Ejected {
		fmt.Printf("ejected %s: %s\n", e.Branch, e.Reason)
	}
	for _, n := range res.Notes {
		fmt.Println("note: " + n)
	}
	if len(res.Merged) > 0 {
		fmt.Printf("%s in %s is now at %.12s; nothing pushed\n", res.Base, res.BaseDir, res.Tip)
	}
}

// capturedCheck runs the check quietly and returns its last lines with the
// error, so the queue can name the culprit with the output that convicted it.
func capturedCheck(ctx context.Context, dir, cmdline string) error {
	cmd := sysx.ShellCommand(ctx, cmdline)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) > queueTail {
		lines = lines[len(lines)-queueTail:]
	}
	return errors.Join(fmt.Errorf("check failed: %w", err), errors.New(strings.Join(lines, "\n")))
}
