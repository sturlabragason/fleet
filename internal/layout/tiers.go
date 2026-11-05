package layout

// TierGrid is the overview past one screen of cards (DESIGN.md §5e, ADR-32):
// at most one row of full cards, then an area of one-line rows.
type TierGrid struct {
	Cards, TileW int // full cards in the card row and their width (0 = no card row)
	Top          int // first line of the row area, below the cards
	Lines        int // lines in the row area
	Cols, CellW  int // row cells per line and their width
}

// RowMinW and RowGap size the one-line rows: cells of at least RowMinW
// columns, RowGap columns apart.
const (
	RowMinW = 30
	RowGap  = 2
)

// Tiered reports whether n agents in a w×h area (the overview below its
// header) switch to tiers: not every card fits, and there is room for a
// card row and at least one line of rows. A pane shorter than that keeps
// the chips line.
func (c Config) Tiered(w, h, n int) bool {
	return n > 0 && w > 0 && h > c.TileH && c.Tiles(w, h, n).Cards < n
}

// Tiers lays out a w×h area for `urgent` agents that want full cards: one
// card row holding up to w/TileW of them, the rest of the height for rows.
// With no urgent agent the rows take the whole area.
func (c Config) Tiers(w, h, urgent int) TierGrid {
	g := TierGrid{Lines: max(h, 0)}
	if urgent > 0 && h > c.TileH {
		g.Cards = min(urgent, max(w/c.TileW, 1))
		g.TileW = max(min(w/g.Cards, c.TileMaxW), min(w, c.TileW))
		g.Top, g.Lines = c.TileH, h-c.TileH
	}
	g.Cols, g.CellW = RowCols(w)
	return g
}

// RowCols splits w columns into row cells of at least RowMinW, RowGap
// apart; narrower than RowMinW it is one cell of w.
func RowCols(w int) (cols, cellW int) {
	cols = max(1, (w+RowGap)/(RowMinW+RowGap))
	return cols, max((w-RowGap*(cols-1))/cols, 0)
}
