package graph

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// StaleCommits is how far the checkout may move past the graph's commit
// before fleet stops using the graph without being asked (ADR-165).
const StaleCommits = 100

// ErrNoGraph and ErrStale are why Fresh gives no graph; callers fall back to
// what they did without one.
var (
	ErrNoGraph = errors.New("no code graph for this repo; run fleet graph")
	ErrStale   = errors.New("code graph is stale")
)

// Fresh is the out dir of dir's graph when there is one and its commit is
// at most StaleCommits behind the main checkout's HEAD (ADR-165). A commit
// git doesn't know counts as stale.
func Fresh(ctx context.Context, home, dir string) (string, error) {
	st, out, ok := Find(home, dir)
	if !ok {
		return "", ErrNoGraph
	}
	if st.Commit == "" {
		return "", fmt.Errorf("%w: no commit in its stamp", ErrStale)
	}
	b, err := exec.CommandContext(ctx, "git", "-C", st.Root, "rev-list", "--count", st.Commit+"..HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("%w: commit %.8s unknown", ErrStale, st.Commit)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if n > StaleCommits {
		return "", fmt.Errorf("%w: %d commits behind; fleet graph rebuilds it", ErrStale, n)
	}
	return out, nil
}

// readsFile is the graph read log under home: one JSON line per read.
func readsFile(home string) string { return filepath.Join(home, "graphs", "reads.jsonl") }

// Read is one line of reads.jsonl: fleet read a graph for use (touching,
// merge, recall, brief), on its own (Auto) or because it was asked, and
// handed back N results.
type Read struct {
	At    time.Time `json:"at"`
	Use   string    `json:"use"`
	Auto  bool      `json:"auto,omitempty"`
	N     int       `json:"n"`
	Agent string    `json:"agent,omitempty"`
}

// Record appends one read to reads.jsonl, the usage measure of ADR-165. A
// failed write is logged, never returned: the measure must not break a read.
func Record(home, use string, auto bool, n int) {
	agent, _, _ := strings.Cut(os.Getenv(sysx.OwnerEnv), "@")
	b, _ := json.Marshal(Read{At: time.Now().Truncate(time.Second), Use: use, Auto: auto, N: n, Agent: agent})
	p := readsFile(home)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.Write(append(b, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		logx.For("graphify").Warn("graph read not recorded", "file", p, "err", err)
	}
}

// Reads counts the reads since t by use, for doctor.
func Reads(home string, since time.Time) map[string]int {
	f, err := os.Open(readsFile(home))
	if err != nil {
		return nil
	}
	defer f.Close()
	m := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Read
		if json.Unmarshal(sc.Bytes(), &r) == nil && !r.At.Before(since) {
			m[r.Use]++
		}
	}
	return m
}

// briefUsers is how many users the spawn brief names per file.
const briefUsers = 8

// Brief is the code-graph note for a spawn prompt (ADR-165): for each repo
// path the prompt names that the graph holds, the files that use it. Empty
// when the prompt names no such path or there is no fresh graph; the graph
// is read only when a named path exists in the repo.
func Brief(ctx context.Context, home, dir, prompt string) string {
	root, err := Root(dir)
	if err != nil {
		return ""
	}
	paths := promptPaths(root, prompt)
	if len(paths) == 0 {
		return ""
	}
	out, err := Fresh(ctx, home, dir)
	if err != nil {
		return ""
	}
	d, err := LoadDeps(out)
	if err != nil {
		logx.For("graphify").Warn("graph unreadable", "out", out, "err", err)
		return ""
	}
	var b strings.Builder
	n := 0
	for _, p := range paths {
		users := d.UsedBy(underPath(d, p))
		if len(users) == 0 {
			continue
		}
		n++
		more := ""
		if len(users) > briefUsers {
			more = fmt.Sprintf(" and %d more", len(users)-briefUsers)
			users = users[:briefUsers]
		}
		fmt.Fprintf(&b, "- %s is used by %s%s\n", p, strings.Join(users, ", "), more)
	}
	Record(home, "brief", true, n)
	if n == 0 {
		return ""
	}
	return "Code graph (callers of the paths above, from fleet's graph of main; `fleet recall <path>` shows who changed them):\n" + b.String()
}

// promptPaths are the relative repo paths (with a slash, inside root, that
// exist) the prompt names, once each, in prompt order.
func promptPaths(root, prompt string) []string {
	var paths []string
	seen := map[string]bool{}
	for w := range strings.FieldsSeq(prompt) {
		w = path.Clean(strings.Trim(w, "`'\"()[]{}<>,;:"))
		if !strings.Contains(w, "/") || strings.HasPrefix(w, "/") || strings.HasPrefix(w, "..") || seen[w] || !exists(filepath.Join(root, w)) {
			continue
		}
		seen[w] = true
		paths = append(paths, w)
	}
	return paths
}

// underPath is p itself when it is a file in d, else every file in d under
// the directory p.
func underPath(d Deps, p string) []string {
	set := map[string]bool{}
	for from, tos := range d {
		for f := range tos {
			for _, x := range []string{from, f} {
				if x == p || strings.HasPrefix(x, p+"/") {
					set[x] = true
				}
			}
		}
	}
	files := make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}
