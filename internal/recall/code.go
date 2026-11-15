package recall

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/adr"
)

// Owners links a commit on main to the fleet agent that made it (ADR-136).
type Owners struct {
	Reports []Msg          // every report, oldest first; an agent's last one goes beside its commits
	ADR     map[int]string // ADR number -> the agent that claimed it (adr-claims.json)
}

var adrRef = regexp.MustCompile(`ADR-(\d+)`)

// Touched answers "what touched these files": the commits on main's
// first-parent line that changed them, newest first, at most limit. The
// agent is the commit's Fleet-Agent trailer, which fleet merge adds
// (ADR-137), else the name in a `Merge fleet/<name>: subject` commit (Kind
// "merge"), else the fleet/<name> branch whose tip the commit is, else the
// agent that claimed the first ADR the subject names; its last report's
// first line is the hit's Text.
func Touched(ctx context.Context, repo string, files []string, o Owners, limit int) ([]Hit, error) {
	args := append([]string{"-C", repo, "--literal-pathspecs", "log", "--first-parent", "main", fmt.Sprintf("-n%d", limit), "--format=%H%x1f%cI%x1f%(trailers:key=Fleet-Agent,valueonly,separator=%x1e)%x1f%s", "--"}, files...)
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	tips := branchTips(ctx, repo)
	last := map[string]string{}
	for _, m := range o.Reports {
		last[m.From] = firstLine(m.Text)
	}
	var hits []Hit
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(l, "\x1f", 4)
		if len(f) != 4 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f[1])
		h := Hit{Kind: "commit", At: at.Local(), Title: f[3], Who: tips[f[0]]}
		if rest, ok := strings.CutPrefix(f[3], "Merge fleet/"); ok {
			if name, title, ok := strings.Cut(rest, ": "); ok {
				h.Kind, h.Who, h.Title = "merge", name, title
			}
		}
		if who := fleetAgent(f[2]); who != "" {
			h.Who = who
		}
		for _, m := range adrRef.FindAllStringSubmatch(f[3], -1) {
			if n, _ := strconv.Atoi(m[1]); h.Who == "" && o.ADR[n] != "" {
				h.Who = o.ADR[n]
			}
		}
		h.Text = last[h.Who]
		hits = append(hits, h)
	}
	return hits, nil
}

// fleetAgent is the first Fleet-Agent trailer value in git's
// %(trailers:key=Fleet-Agent,valueonly,separator=%x1e) output.
func fleetAgent(s string) string {
	for v := range strings.SplitSeq(s, "\x1e") {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// branchTips maps the commit at the tip of each fleet/<name> branch to name;
// an agent's last commit stays its branch's tip after a fast-forward merge.
func branchTips(ctx context.Context, repo string) map[string]string {
	out, err := exec.CommandContext(ctx, "git", "-C", repo, "for-each-ref", "--format=%(objectname) %(refname:short)", "refs/heads/fleet/").Output()
	if err != nil {
		return nil
	}
	tips := map[string]string{}
	for l := range strings.SplitSeq(string(out), "\n") {
		if sha, ref, ok := strings.Cut(l, " "); ok {
			if _, dup := tips[sha]; !dup {
				tips[sha] = strings.TrimPrefix(ref, "fleet/")
			}
		}
	}
	return tips
}

// formatCode is the code section of Format, before the reports.
func (r Result) formatCode(b *strings.Builder) {
	if len(r.CodeFiles) == 0 {
		return
	}
	files := strings.Join(r.CodeFiles, ", ")
	fmt.Fprintf(b, "## code: %d commits to %s\n", len(r.Code), clip(files, 160))
	if r.CodeErr != "" {
		b.WriteString("git log failed: " + r.CodeErr + "\n")
	}
	for _, h := range r.Code {
		who := h.Who
		if who == "" {
			who = "-"
		}
		fmt.Fprintf(b, "%s  %-6s %s: %s\n", h.At.Format("2006-01-02 15:04"), h.Kind, who, clip(h.Title, 160))
		if h.Text != "" {
			fmt.Fprintf(b, "    … %s\n", clip(h.Text, 160))
		}
	}
	b.WriteString("\n")
}

// adrOwners maps every claimed ADR number to its agent.
func adrOwners(home string) map[int]string {
	cs, err := adr.NewStore(home).List(nil)
	if err != nil {
		return nil
	}
	m := map[int]string{}
	for _, c := range cs {
		if c.For != "" {
			m[c.N] = c.For
		}
	}
	return m
}
