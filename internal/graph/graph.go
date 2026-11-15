// Package graph keeps one model-free graphify code graph per repo, outside
// every checkout, and hands it to agents (DESIGN §6.15, ADR-128, ADR-133).
// graphify is an ADR-126 companion: optional, never installed by fleet. The
// graph is built from the main checkout, so it does not show an agent's own
// edits; node paths are repo-relative, so one graph serves every worktree.
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/sysx"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// Spec is the graphify companion: $FLEET_GRAPHIFY, then
// [integrations.graphify] path, then PATH; "off" turns the graph off.
var Spec = companion.Register(companion.Spec{
	Name:    "graphify",
	Env:     []string{"FLEET_GRAPHIFY"},
	Version: []string{"--version"},
	Min:     "0.9.109",
	Feature: "code graph for agents (fleet graph)",
	Hint:    "uv tool install graphifyy",
})

// SchemaVersion is the newest graph.json schema this code reads.
const SchemaVersion = 1

// ErrBusy means another build of the same repo holds the lock; a rerun
// marker was left so that build goes round again when it ends.
var ErrBusy = errors.New("a graph build is already running")

const (
	stampFile   = "fleet-graph.json"
	buildPasses = 3
	buildTime   = 5 * time.Minute
)

// Stamp says what a built graph is; it is read on every spawn instead of the
// ~10 MB graph.json.
type Stamp struct {
	Root            string    `json:"root"`
	BuiltAt         time.Time `json:"built_at"`
	Commit          string    `json:"commit"`
	GraphifyVersion string    `json:"graphify_version"`
	SchemaVersion   int       `json:"schema_version"`
	Nodes           int       `json:"nodes"`
	Edges           int       `json:"edges"`
}

// Root is the main checkout of the repo holding dir.
func Root(dir string) (string, error) {
	common, err := worktree.CommonDir(dir)
	if err != nil {
		return "", err
	}
	if filepath.Base(common) != ".git" {
		return "", errors.New("not in a non-bare git repo")
	}
	return filepath.Dir(common), nil
}

// Key makes root one file name.
func Key(root string) string {
	k := strings.NewReplacer("/", "-", `\`, "-").Replace(strings.TrimLeft(root, `/\`))
	if k == "" {
		return "root"
	}
	return k
}

// Dir is where root's graph lives: <home>/graphs/<key>.
func Dir(home, root string) string { return filepath.Join(home, "graphs", Key(root)) }

// Find reads the stamp of the graph for the repo holding dir and returns it
// with the graph's out dir. ok is false when there is no usable graph.
func Find(home, dir string) (Stamp, string, bool) {
	root, err := Root(dir)
	if err != nil {
		return Stamp{}, "", false
	}
	out := Dir(home, root)
	st, ok := readStamp(out)
	if !ok {
		return Stamp{}, "", false
	}
	if _, err := os.Stat(filepath.Join(out, "graph.json")); err != nil {
		return Stamp{}, "", false
	}
	return st, out, true
}

func readStamp(out string) (Stamp, bool) {
	b, err := os.ReadFile(filepath.Join(out, stampFile))
	if err != nil {
		return Stamp{}, false
	}
	var st Stamp
	if json.Unmarshal(b, &st) != nil || st.SchemaVersion > SchemaVersion {
		return Stamp{}, false
	}
	return st, true
}

// Build runs graphify over root into its out dir, and writes the stamp only
// after a good build. A failed build keeps the previous graph and stamp.
// A rerun asked for while the lock was being released is taken up here, after
// the unlock, so it is never lost; one left after the pass cap is dropped.
func Build(ctx context.Context, home, root string, force bool) (Stamp, error) {
	rerun := filepath.Join(filepath.Dir(Dir(home, root)), Key(root)+".rerun")
	for round := 0; ; round++ {
		st, err := buildLocked(ctx, home, root, force)
		if err != nil || !exists(rerun) {
			return st, err
		}
		if round+1 >= buildPasses {
			_ = os.Remove(rerun)
			logx.For("graphify").Warn("graph rebuild requests kept coming; the graph may be behind main until the next build", "root", root)
			return st, nil
		}
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func buildLocked(ctx context.Context, home, root string, force bool) (Stamp, error) {
	log := logx.For("graphify")
	out := Dir(home, root)
	gdir := filepath.Dir(out)
	if err := os.MkdirAll(gdir, 0o755); err != nil {
		return Stamp{}, err
	}
	base := filepath.Join(gdir, Key(root))
	lock, err := os.OpenFile(base+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Stamp{}, err
	}
	defer lock.Close()
	if err := sysx.Flock(lock, sysx.LockEx|sysx.LockNB); err != nil {
		_ = os.WriteFile(base+".rerun", nil, 0o644)
		return Stamp{}, ErrBusy
	}
	defer sysx.Flock(lock, sysx.LockUn)
	tool, err := companion.Find(Spec)
	if err != nil {
		return Stamp{}, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return Stamp{}, err
	}
	var st Stamp
	for pass := 0; pass < buildPasses; pass++ {
		_ = os.Remove(base + ".rerun")
		args := []string{"update", root}
		if force {
			args = append(args, "--force")
		}
		start := time.Now()
		if _, err := tool.Run(ctx, companion.Opts{Timeout: buildTime, Low: true, Clean: true, Env: []string{"GRAPHIFY_OUT=" + out}}, args...); err != nil {
			log.Warn("graph build failed", "root", root, "err", err)
			return Stamp{}, err
		}
		st, err = stampFrom(root, out)
		if err != nil {
			log.Warn("graph unreadable after build", "root", root, "err", err)
			return Stamp{}, err
		}
		if err := writeStamp(out, st); err != nil {
			return Stamp{}, err
		}
		log.Info("graph built", "root", root, "nodes", st.Nodes, "edges", st.Edges, "commit", st.Commit, "secs", time.Since(start).Seconds())
		if _, err := os.Stat(base + ".rerun"); err != nil {
			break
		}
	}
	return st, nil
}

func stampFrom(root, out string) (Stamp, error) {
	b, err := os.ReadFile(filepath.Join(out, "graph.json"))
	if err != nil {
		return Stamp{}, err
	}
	var g struct {
		Graph struct {
			SchemaVersion   int    `json:"schema_version"`
			GraphifyVersion string `json:"graphify_version"`
		} `json:"graph"`
		Nodes  []json.RawMessage `json:"nodes"`
		Links  []json.RawMessage `json:"links"`
		Commit string            `json:"built_at_commit"`
	}
	if err := json.Unmarshal(b, &g); err != nil {
		return Stamp{}, fmt.Errorf("graph.json: %w", err)
	}
	if g.Graph.SchemaVersion > SchemaVersion {
		return Stamp{}, fmt.Errorf("graphify too new: graph schema %d, fleet reads up to %d", g.Graph.SchemaVersion, SchemaVersion)
	}
	return Stamp{Root: root, BuiltAt: time.Now().Truncate(time.Second), Commit: g.Commit,
		GraphifyVersion: g.Graph.GraphifyVersion, SchemaVersion: g.Graph.SchemaVersion,
		Nodes: len(g.Nodes), Edges: len(g.Links)}, nil
}

func writeStamp(out string, st Stamp) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(out, stampFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(out, stampFile))
}

// Line is the one playbook line that hands the graph to an agent.
func Line(st Stamp) string {
	return fmt.Sprintf("A code graph of this repo, built from `main` at %s, is at `$GRAPHIFY_OUT`: "+
		"try `graphify affected <file>`, `graphify path \"A\" \"B\"` or `graphify explain \"X\"` before a wide search. "+
		"It does not show your own edits; don't rebuild it.", st.BuiltAt.Local().Format("2006-01-02 15:04"))
}

// BuildMode is [integrations.graphify] build: "merge" (default) or "manual".
func BuildMode(p *prefs.Prefs) string {
	switch v := strings.TrimSpace(p.Integration("graphify").Values["build"]); v {
	case "", "merge":
		return "merge"
	case "manual":
		return "manual"
	default:
		logx.For("graphify").Warn("unknown [integrations.graphify] build; using merge", "build", v)
		return "merge"
	}
}

// List is every stamp under <home>/graphs, for doctor.
func List(home string) []Stamp {
	files, _ := filepath.Glob(filepath.Join(home, "graphs", "*", stampFile))
	var out []Stamp
	for _, f := range files {
		if st, ok := readStamp(filepath.Dir(f)); ok {
			out = append(out, st)
		}
	}
	return out
}
