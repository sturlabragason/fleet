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
// package, tests included), and so is code depending on a test file, and a
// document naming code (DESIGN.md citing a file is not a dependency, and it
// made every docs change look coupled to that code; ADR-207).
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
	return g.deps(), nil
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

// files is where name lives in the graph, sorted: a repo path (a
// file, or a directory and everything under it), a file's base name, a
// symbol (Func, Type, Method) or pkg.Symbol, whose package is the file's
// directory name; when no package matches, the part before the dot is taken
// as a type (Type.Method, whose label carries no receiver) and every file
// with that method counts. Only code nodes count as symbols.
func (g *graphJSON) files(name string) []string {
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
		return nil
	}
	return files
}

// deps is the graph's file dependencies (see LoadDeps).
func (g *graphJSON) deps() Deps {
	file := make(map[string]string, len(g.Nodes))
	doc := map[string]bool{}
	for _, n := range g.Nodes {
		file[n.ID] = n.File
		doc[n.ID] = n.Type == "document"
	}
	d := Deps{}
	for _, l := range g.Links {
		from, to := file[l.Source], file[l.Target]
		if !depRelations[l.Relation] || doc[l.Source] || from == "" || to == "" || from == to || strings.HasSuffix(to, "_test.go") {
			continue
		}
		if d[from] == nil {
			d[from] = map[string]bool{}
		}
		d[from][to] = true
	}
	return d
}

// UsedBy lists, sorted, the files outside files that use one of them.
func (d Deps) UsedBy(files []string) []string {
	in := make(map[string]bool, len(files))
	for _, f := range files {
		in[f] = true
	}
	var users []string
	for from, tos := range d {
		if in[from] {
			continue
		}
		for _, f := range files {
			if tos[f] {
				users = append(users, from)
				break
			}
		}
	}
	sort.Strings(users)
	return users
}

// Lookup is what Resolve found for a recall topic.
type Lookup struct {
	Root   string   // the main checkout, "" outside a repo
	Files  []string // the repo files the topic names
	UsedBy []string // files that use them, from the graph
}

// Resolve turns the words of a recall topic that name code (a path, a
// file, pkg.Symbol or an exported name) into repo files, sorted: through
// the graph in home when the repo holding dir has one, and words that are
// paths in the repo. With a graph it also lists the files that use them
// (ADR-165), and records the read.
func Resolve(home, dir string, words []string) Lookup {
	root, err := Root(dir)
	if err != nil {
		return Lookup{}
	}
	var g *graphJSON
	asked := false
	for _, w := range words {
		asked = asked || codeWord(w)
	}
	if _, out, ok := Find(home, dir); ok && asked {
		if g, err = load(out); err != nil {
			logx.For("graphify").Warn("graph unreadable", "out", out, "err", err)
		}
	}
	set := map[string]bool{}
	for _, w := range words {
		if !codeWord(w) {
			continue
		}
		var fs []string
		if g != nil {
			fs = g.files(w)
			for _, f := range fs {
				set[f] = true
			}
		}
		// A file the graph doesn't hold (new since the build, or not code) is still a path.
		if len(fs) == 0 && strings.ContainsAny(w, "/.") && exists(filepath.Join(root, w)) {
			set[path.Clean(w)] = true
		}
	}
	l := Lookup{Root: root}
	for f := range set {
		l.Files = append(l.Files, f)
	}
	sort.Strings(l.Files)
	if g != nil {
		l.UsedBy = g.deps().UsedBy(l.Files)
		Record(home, "recall", true, len(l.Files))
	}
	return l
}

// codeWord: a path, pkg.Symbol or exported name. A URL or a host path
// (github.com/owner/repo) is not code: scripts that recall repo URLs made
// 251 of 256 recall reads empty graph loads (ADR-207).
func codeWord(w string) bool {
	if host, _, ok := strings.Cut(w, "/"); strings.Contains(w, "://") || ok && strings.Contains(strings.Trim(host, "."), ".") {
		return false
	}
	return strings.ContainsAny(w, "/.") || (w != "" && unicode.IsUpper(rune(w[0])))
}
