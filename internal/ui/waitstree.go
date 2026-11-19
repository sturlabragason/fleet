package ui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/sturlabragason/fleet/internal/waits"
)

// Group levels of the w tree (ADR-168).
const (
	lvlSection = "section"
	lvlCtx     = "ctx"
	lvlParent  = "parent"
)

// wrow is one line of the w view: a group (section, context or parent)
// with its count, or an item.
type wrow struct {
	key    string
	depth  int
	level  string // "" for an item
	name   string
	n      int
	folded bool
	peek   []string // a folded section's first parents
	item   *waits.Item
}

// level is a group level: how it names an item's group and how it orders
// the groups.
type level struct {
	kind  string
	name  func(waits.Item) string
	order func(a, b string) int
}

var (
	bySection = level{lvlSection, func(it waits.Item) string { return it.Section }, func(a, b string) int {
		return cmp.Compare(sectionRank(a), sectionRank(b))
	}}
	byContext = level{lvlCtx, func(it waits.Item) string { return it.Ctx }, func(a, b string) int {
		return cmp.Or(cmp.Compare(noCtx(a), noCtx(b)), cmp.Compare(a, b))
	}}
	byParent = level{lvlParent, func(it waits.Item) string { return it.Parent }, nil} // in the sort's order
)

// newest orders the most recent first.
func newest(a, b waits.Item) int { return b.Since.Compare(a.Since) }

// waitSorts are the w view's row orders, which s cycles; newest first is
// the default. Sections and contexts keep their own order; the items, and
// so the parents, within them follow the sort.
var waitSorts = []struct {
	name string
	cmp  func(a, b waits.Item) int
}{
	{"newest first", newest},
	{"oldest first", func(a, b waits.Item) int { return a.Since.Compare(b.Since) }},
	{"by context", func(a, b waits.Item) int {
		return cmp.Or(cmp.Compare(noCtx(a.Ctx), noCtx(b.Ctx)), cmp.Compare(a.Ctx, b.Ctx), newest(a, b))
	}},
	{"by source", func(a, b waits.Item) int { return cmp.Or(cmp.Compare(a.Source, b.Source), newest(a, b)) }},
}

// noCtx sorts the group of items with no context last.
func noCtx(c string) int {
	if c == "" {
		return 1
	}
	return 0
}

func sectionRank(s string) int {
	if i := slices.Index(waits.Sections, s); i >= 0 {
		return i
	}
	return len(waits.Sections)
}

// rebuild lays the rows out again and keeps the cursor on the same row,
// else at the same place.
func (v *waitsView) rebuild() {
	var key string
	if r, ok := v.cur(); ok {
		key = r.key
	}
	items := v.shown()
	v.rows = v.rows[:0]
	if v.flat {
		for _, it := range waits.Only(items, waits.Waiting) {
			v.rows = append(v.rows, itemRow(it, 0))
		}
	} else {
		lv := []level{bySection, byContext, byParent}
		if v.byCtx {
			lv[0], lv[1] = byContext, bySection
		}
		v.group(items, lv, 0, "")
	}
	for i, r := range v.rows {
		if r.key == key {
			v.sel = i
			return
		}
	}
	v.sel = min(v.sel, max(len(v.rows)-1, 0))
}

func itemRow(it waits.Item, depth int) wrow {
	return wrow{key: "i/" + it.Kind + "\x00" + it.Source + "\x00" + it.Ask + "\x00" + it.Ref, depth: depth, item: &it}
}

// shown is the list under the context chip and the filter.
func (v *waitsView) shown() []waits.Item {
	f := strings.ToLower(v.filter)
	var out []waits.Item
	for _, it := range v.all {
		if v.ctx != "" && it.Ctx != v.ctx {
			continue
		}
		if f != "" && !strings.Contains(strings.ToLower(it.Kind+" "+it.Source+" "+it.Parent+" "+it.Ask), f) {
			continue
		}
		out = append(out, it)
	}
	slices.SortStableFunc(out, waitSorts[v.sort].cmp)
	return out
}

// group adds a row per group of items at lv[0], each followed, unless
// folded, by its subgroups or, at the last level, its items.
func (v *waitsView) group(items []waits.Item, lv []level, depth int, prefix string) {
	if len(lv) == 0 {
		for _, it := range items {
			v.rows = append(v.rows, itemRow(it, depth))
		}
		return
	}
	l := lv[0]
	var names []string
	by := map[string][]waits.Item{}
	for _, it := range items {
		n := l.name(it)
		if _, ok := by[n]; !ok {
			names = append(names, n)
		}
		by[n] = append(by[n], it)
	}
	if l.order != nil {
		slices.SortStableFunc(names, l.order)
	}
	for _, n := range names {
		r := wrow{key: prefix + "/" + l.kind + ":" + n, depth: depth, level: l.kind, name: n, n: len(by[n])}
		r.folded = v.isFolded(r)
		if r.folded && l.kind == lvlSection {
			r.peek = parents(by[n])
		}
		v.rows = append(v.rows, r)
		if !r.folded {
			v.group(by[n], lv[1:], depth+1, r.key)
		}
	}
}

// isFolded is the user's fold, else the default: Running, Quiet and Paused
// start folded, as they hold the most rows and need the least attention.
func (v *waitsView) isFolded(r wrow) bool {
	if f, ok := v.folded[r.key]; ok {
		return f
	}
	return r.level == lvlSection && r.name != waits.Waiting && r.name != waits.Open
}

// parents is the distinct parents of items, first seen first.
func parents(items []waits.Item) []string {
	var out []string
	for _, it := range items {
		if !slices.Contains(out, it.Parent) {
			out = append(out, it.Parent)
		}
	}
	return out
}

func (v *waitsView) toggle(r wrow) {
	v.folded[r.key] = !r.folded
	v.rebuild()
}

// fold folds (←) or unfolds (→) the selected group; ← on an item or a
// folded group moves to its parent group.
func (v *waitsView) fold(shut bool) {
	r, ok := v.cur()
	if !ok {
		return
	}
	if r.item == nil && r.folded != shut {
		v.toggle(r)
		return
	}
	if shut {
		for i := v.sel - 1; i >= 0; i-- {
			if v.rows[i].depth < r.depth {
				v.sel = i
				return
			}
		}
	}
}

// jump moves to the first row of section s and unfolds it (1-4).
func (v *waitsView) jump(s string) {
	if v.flat {
		v.flat = false
		v.rebuild()
	}
	for i, r := range v.rows {
		if r.level == lvlSection && r.name == s {
			v.sel = i
			if r.folded {
				v.toggle(r)
			}
			return
		}
	}
}

// contexts is every context in the list with its count, in chip order.
func (v *waitsView) contexts() ([]string, map[string]int) {
	n := map[string]int{}
	var names []string
	for _, it := range v.all {
		if it.Ctx == "" {
			continue
		}
		if n[it.Ctx] == 0 {
			names = append(names, it.Ctx)
		}
		n[it.Ctx]++
	}
	slices.Sort(names)
	return names, n
}

// nextCtx is c: the next context chip, after the last one all again.
func (v *waitsView) nextCtx() {
	names, _ := v.contexts()
	i := slices.Index(names, v.ctx)
	v.ctx = ""
	if i+1 < len(names) {
		v.ctx = names[i+1]
	}
	v.rebuild()
}
