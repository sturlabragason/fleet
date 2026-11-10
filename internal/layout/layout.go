package layout

import (
	"fmt"
	"strings"
)

// Node is a layout cell: a leaf (Pane != "") or a split.
type Node struct {
	W, H, X, Y int
	Pane       string // "%12" for leaves
	Horiz      bool   // children side by side ({}) vs stacked ([])
	Kids       []*Node
	pct        int // first child's share (main+stack preset)
}

// Input describes the window to lay out.
type Input struct {
	W, H int
	Sups []string // admiral pane ids, main first (none if hidden/absent), stacked in one column (DESIGN §6.12)
	// SupRows are the admiral heights, parallel to Sups, as the user set
	// them; nil means equal rows. Config.SupRowsFor fits them to the column.
	SupRows []int
	Ov      string   // overview pane id
	Wall    []string // wall pane ids in slot order
	Preset  string
	Focus   string // emphasis pane id (must be on the wall), "" for an even grid
}

// Build returns the full layout tree.
func (c Config) Build(in Input) *Node {
	root := &Node{W: in.W, H: in.H}
	right := root
	if len(in.Sups) > 0 && !c.SupHidden {
		sw := c.supWidth(in.W)
		sup := &Node{Pane: in.Sups[0]}
		if len(in.Sups) > 1 { // equal rows, 1-row borders between; place() sizes them
			sup = &Node{}
			for _, p := range in.Sups {
				sup.Kids = append(sup.Kids, leaf(p))
			}
		}
		right = &Node{}
		root.Horiz = true
		if c.SupSide == "right" {
			root.Kids = []*Node{right, sup}
		} else {
			root.Kids = []*Node{sup, right}
		}
		sizes(root, []int{sw, in.W - sw - 1}, c.SupSide == "right")
		if len(in.Sups) > 1 {
			if rows := c.SupRowsFor(in.SupRows, len(in.Sups), sup.H); rows != nil {
				sizes(sup, rows, false)
			}
		}
	}
	ov := &Node{Pane: in.Ov}
	if len(in.Wall) == 0 {
		*right = Node{W: right.W, H: right.H, X: right.X, Y: right.Y, Pane: in.Ov}
		return root
	}
	wall := &Node{}
	right.Horiz = false
	right.Kids = []*Node{ov, wall}
	_, wh := c.WallRect(in.W, in.H, false)
	oh := right.H - wh - 1
	sizes(right, []int{oh, wh}, false)
	// The wall rectangle is now known, so the grid can be sized against it.
	tree := c.wallTree(in.Wall, in.Preset, in.Focus, wall.W, wall.H)
	tree.W, tree.H, tree.X, tree.Y = wall.W, wall.H, wall.X, wall.Y
	right.Kids[1] = tree
	return root
}

func (c Config) wallTree(panes []string, preset, focus string, w, h int) *Node {
	if len(panes) == 1 {
		return leaf(panes[0])
	}
	if preset == "m" {
		stack := &Node{}
		for _, p := range panes[1:] {
			stack.Kids = append(stack.Kids, leaf(p))
		}
		if len(stack.Kids) == 1 {
			stack = stack.Kids[0]
		}
		return &Node{Horiz: true, Kids: []*Node{leaf(panes[0]), stack}, pct: c.MainPercent}
	}
	if preset != "1" && focus != "" {
		if n := c.emphasisTree(panes, focus, w, h); n != nil {
			return n
		}
	}
	return c.gridTree(panes, w, h)
}

// emphasisTree gives the focus pane FocusPercent of the wall and grids the
// rest in the remainder (DESIGN.md §5b.3, ADR-16). It returns nil when the
// remainder cannot hold the other panes at the minimum pane size, or when the
// focus pane is not on the wall.
func (c Config) emphasisTree(panes []string, focus string, w, h int) *Node {
	var others []string
	for _, p := range panes {
		if p != focus {
			others = append(others, p)
		}
	}
	if len(others) == len(panes) || len(others) == 0 {
		return nil
	}
	// Stacked: focus on top, full width. Side by side: focus on the left.
	stackedH := h - 1 - part(h, c.FocusPercent)
	sideW := w - 1 - part(w, c.FocusPercent)
	_, stacked := gridShape(len(others), w, stackedH, c)
	_, side := gridShape(len(others), sideW, h, c)
	horiz := side > stacked // tie goes to stacked: it keeps full code width
	best, restW, restH := stacked, w, stackedH
	if horiz {
		best, restW, restH = side, sideW, h
	}
	focusW, focusH := w, part(h, c.FocusPercent)
	if horiz {
		focusW, focusH = part(w, c.FocusPercent), h
	}
	if best < 1 || score(focusW, focusH, c) < 1 {
		return nil
	}
	return &Node{Horiz: horiz, pct: c.FocusPercent,
		Kids: []*Node{leaf(focus), c.gridTree(others, restW, restH)}}
}

// part is the first child's size of a pct split of total (see place).
func part(total, pct int) int { return (total - 1) * pct / 100 }

// gridTree lays panes out in the auto-grid for a w×h rectangle.
func (c Config) gridTree(panes []string, w, h int) *Node {
	if len(panes) == 1 {
		return leaf(panes[0])
	}
	perRow, _ := gridShape(len(panes), w, h, c)
	var rows []*Node
	i := 0
	for _, n := range perRow {
		row := &Node{Horiz: true}
		for j := i; j < i+n && j < len(panes); j++ {
			row.Kids = append(row.Kids, leaf(panes[j]))
		}
		i += n
		if len(row.Kids) == 0 {
			continue
		}
		if len(row.Kids) == 1 {
			row = row.Kids[0]
		}
		rows = append(rows, row)
	}
	if len(rows) == 1 {
		return rows[0]
	}
	return &Node{Kids: rows}
}

func leaf(p string) *Node { return &Node{Pane: p} }

// sizes sets the parent's children sizes along its axis; fixed[0] is the first child's size.
// If fromEnd, the first fixed size applies to the last child instead.
func sizes(n *Node, fixed []int, fromEnd bool) {
	if fromEnd {
		fixed = []int{fixed[1], fixed[0]}
	}
	pos := n.X
	if !n.Horiz {
		pos = n.Y
	}
	for i, k := range n.Kids {
		if n.Horiz {
			k.X, k.Y, k.W, k.H = pos, n.Y, fixed[i], n.H
		} else {
			k.X, k.Y, k.W, k.H = n.X, pos, n.W, fixed[i]
		}
		pos += fixed[i] + 1
	}
}

// split divides total into n parts separated by 1-cell borders.
func split(total, n int) []int {
	avail := total - (n - 1)
	out := make([]int, n)
	for i := range out {
		out[i] = avail / n
		if i < avail%n {
			out[i]++
		}
	}
	return out
}

// place fills in geometry for subtrees whose children have no size yet.
func place(n *Node) {
	if len(n.Kids) == 0 {
		return
	}
	if k := n.Kids[0]; k.W == 0 && k.H == 0 {
		total := n.H
		if n.Horiz {
			total = n.W
		}
		parts := split(total, len(n.Kids))
		if n.pct > 0 && len(n.Kids) == 2 {
			first := (total - 1) * n.pct / 100
			parts = []int{first, total - 1 - first}
		}
		sizes(n, parts, false)
	}
	for _, k := range n.Kids {
		place(k)
	}
}

// String renders the tmux layout body (without checksum).
func (n *Node) String() string {
	s := fmt.Sprintf("%dx%d,%d,%d", n.W, n.H, n.X, n.Y)
	if len(n.Kids) == 0 {
		return s + "," + strings.TrimPrefix(n.Pane, "%")
	}
	open, close := "[", "]"
	if n.Horiz {
		open, close = "{", "}"
	}
	var ks []string
	for _, k := range n.Kids {
		ks = append(ks, k.String())
	}
	return s + open + strings.Join(ks, ",") + close
}

// Leaves returns pane ids in traversal order (the order tmux assigns panes).
func (n *Node) Leaves() []string {
	if len(n.Kids) == 0 {
		return []string{n.Pane}
	}
	var out []string
	for _, k := range n.Kids {
		out = append(out, k.Leaves()...)
	}
	return out
}

// Checksum is tmux's layout checksum.
func Checksum(s string) string {
	var csum uint16
	for i := 0; i < len(s); i++ {
		csum = (csum >> 1) + ((csum & 1) << 15)
		csum += uint16(s[i])
	}
	return fmt.Sprintf("%04x", csum)
}

// Render returns the full "csum,body" string for select-layout.
func (c Config) Render(in Input) (string, []string) {
	root := c.Build(in)
	place(root)
	body := root.String()
	return Checksum(body) + "," + body, root.Leaves()
}
