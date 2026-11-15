package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// LoadDeps reads the file dependencies from the graph.json in out.
func LoadDeps(out string) (Deps, error) {
	b, err := os.ReadFile(filepath.Join(out, "graph.json"))
	if err != nil {
		return nil, fmt.Errorf("read graph: %w", err)
	}
	var g struct {
		Nodes []struct {
			ID   string `json:"id"`
			File string `json:"source_file"`
		} `json:"nodes"`
		Links []struct {
			Source   string `json:"source"`
			Target   string `json:"target"`
			Relation string `json:"relation"`
		} `json:"links"`
	}
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("parse graph: %w", err)
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
