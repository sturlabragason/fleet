package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// Deps is a graph's code dependencies between files: Deps[a][b] means code
// in a calls, references, embeds or dispatches to code in b (ADR-136).
// Package imports are left out (graphify links them to every file of the
// package, tests included), and so is code depending on a test file.
type Deps map[string]map[string]bool

// depRelations are the graphify edge kinds that make one file depend on
// another.
var depRelations = map[string]bool{
	"calls": true, "references": true, "embeds": true, "method": true,
	"dispatches_to": true, "indirect_call": true,
}

// graphJSON is the part of graph.json fleet reads beyond the stamp.
type graphJSON struct {
	Nodes []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		File  string `json:"source_file"`
		Type  string `json:"file_type"`
	} `json:"nodes"`
	Links []struct {
		Source   string `json:"source"`
		Target   string `json:"target"`
		Relation string `json:"relation"`
	} `json:"links"`
}

func load(out string) (*graphJSON, error) {
	b, err := os.ReadFile(filepath.Join(out, "graph.json"))
	if err != nil {
		return nil, fmt.Errorf("read graph: %w", err)
	}
	var g graphJSON
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("parse graph: %w", err)
	}
	return &g, nil
}

// LoadDeps reads the file dependencies from the graph.json in out.
func LoadDeps(out string) (Deps, error) {
	g, err := load(out)
	if err != nil {
		return nil, err
	}
	file := make(map[string]string, len(g.Nodes))
	for _, n := range g.Nodes {
		file[n.ID] = n.File
	}
	d := Deps{}
	for _, l := range g.Links {
		from, to := file[l.Source], file[l.Target]
		if !depRelations[l.Relation] || from == "" || to == "" || from == to || strings.HasSuffix(to, "_test.go") {
			continue
		}
		if d[from] == nil {
			d[from] = map[string]bool{}
		}
		d[from][to] = true
	}
	return d, nil
}

// Link is one file using another.
type Link struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Between lists, sorted, every file in from that uses a file in to.
func (d Deps) Between(from, to []string) []Link {
	var ls []Link
	for _, f := range from {
		for _, t := range to {
			if d[f][t] {
				ls = append(ls, Link{From: f, To: t})
			}
		}
	}
	sort.Slice(ls, func(i, j int) bool {
		if ls[i].From != ls[j].From {
			return ls[i].From < ls[j].From
		}
		return ls[i].To < ls[j].To
	})
	return ls
}

// Coupling is how another worktree's changes meet the files you change:
// files both change, your files that use its files, and its files that
// use yours. Order says which branch should merge first when only one side
// depends on the other.
type Coupling struct {
	worktree.Worktree
	Overlap []string `json:"overlap,omitempty"`
	Uses    []Link   `json:"uses,omitempty"`
	UsedBy  []Link   `json:"used_by,omitempty"`
	Order   string   `json:"order,omitempty"`
}

// Couple lists the worktrees in ws whose changed files overlap mine or are
// one dependency step from them, in ws's order.
func Couple(d Deps, mine []string, ws []worktree.Worktree) []Coupling {
	var cs []Coupling
	for _, w := range ws {
		c := Coupling{Worktree: w, Uses: d.Between(mine, w.Files), UsedBy: d.Between(w.Files, mine)}
		if o := worktree.Overlap([]worktree.Worktree{w}, mine); len(o) > 0 {
			c.Overlap = o[0].Files
		}
		if c.Overlap == nil && c.Uses == nil && c.UsedBy == nil {
			continue
		}
		switch {
		case c.Uses != nil && c.UsedBy == nil:
			c.Order = "merge " + w.Branch + " first: your files use its changes"
		case c.UsedBy != nil && c.Uses == nil:
			c.Order = "you merge first: its files use yours"
		}
		cs = append(cs, c)
	}
	return cs
}

// CouplingText is Couple's answer for `fleet touching --graph`.
func CouplingText(cs []Coupling) string {
	if len(cs) == 0 {
		return "no other worktree changes these files or code they depend on\n"
	}
	var b strings.Builder
	for _, c := range cs {
		fmt.Fprintf(&b, "%s  %s\n", c.Path, c.Branch)
		for _, f := range c.Overlap {
			fmt.Fprintf(&b, "    both change %s\n", f)
		}
		for _, l := range c.Uses {
			fmt.Fprintf(&b, "    your %s uses its %s\n", l.From, l.To)
		}
		for _, l := range c.UsedBy {
			fmt.Fprintf(&b, "    its %s uses your %s\n", l.From, l.To)
		}
		if c.Order != "" {
			fmt.Fprintf(&b, "    %s\n", c.Order)
		}
	}
	return b.String()
}

// Files is where name lives in the graph in out, sorted: a repo path (a
// file, or a directory and everything under it), a file's base name, a
// symbol (Func, Type, Method) or pkg.Symbol, whose package is the file's
// directory name; when no package matches, the part before the dot is taken
// as a type (Type.Method, whose label carries no receiver) and every file
// with that method counts. Only code nodes count as symbols.
func Files(out, name string) ([]string, error) {
	g, err := load(out)
	if err != nil {
		return nil, err
	}
	pkg, sym, qualified := strings.Cut(name, ".")
	if !qualified || strings.Contains(sym, ".") {
		pkg, sym = "", name
	}
	set, methods := map[string]bool{}, map[string]bool{}
	for _, n := range g.Nodes {
		f := n.File
		switch {
		case f == "":
		case f == name || strings.HasPrefix(f, name+"/") || path.Base(f) == name:
			set[f] = true
		case n.Type == "code" && strings.TrimSuffix(strings.TrimPrefix(n.Label, "."), "()") == sym:
			if pkg == "" || path.Base(path.Dir(f)) == pkg {
				set[f] = true
			} else if strings.HasPrefix(n.Label, ".") {
				methods[f] = true
			}
		}
	}
	if len(set) == 0 {
		set = methods
	}
	files := make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, nil
	}
	return files, nil
}

// Resolve turns the words of a recall topic that name code (a path, a
// file, pkg.Symbol or an exported name) into repo files, sorted: through
// the graph in home when the repo holding dir has one, and words that are
// paths in the repo. root is the main checkout, "" outside a repo.
func Resolve(home, dir string, words []string) (root string, files []string) {
	root, err := Root(dir)
	if err != nil {
		return "", nil
	}
	_, out, haveGraph := Find(home, dir)
	set := map[string]bool{}
	for _, w := range words {
		if !codeWord(w) {
			continue
		}
		var fs []string
		if haveGraph {
			fs, err = Files(out, w)
			if err != nil {
				logx.For("graphify").Warn("graph unreadable", "out", out, "err", err)
				haveGraph = false
			}
			for _, f := range fs {
				set[f] = true
			}
		}
		// A file the graph doesn't hold (new since the build, or not code) is still a path.
		if len(fs) == 0 && strings.ContainsAny(w, "/.") && exists(filepath.Join(root, w)) {
			set[path.Clean(w)] = true
		}
	}
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return root, files
}

func codeWord(w string) bool {
	return strings.ContainsAny(w, "/.") || (w != "" && unicode.IsUpper(rune(w[0])))
}
