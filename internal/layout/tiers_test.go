package layout

import "testing"

func TestTiers(t *testing.T) {
	c := Default
	for _, tc := range []struct {
		name         string
		w, h, urgent int
		want         TierGrid
	}{
		{"strip, 3 waiting", 150, 13, 3, TierGrid{Cards: 3, TileW: 48, Top: 6, Lines: 7, Cols: 4, CellW: 36}},
		{"strip, more urgent than slots", 150, 13, 9, TierGrid{Cards: 5, TileW: 30, Top: 6, Lines: 7, Cols: 4, CellW: 36}},
		{"strip, nobody urgent: rows only", 150, 13, 0, TierGrid{Lines: 13, Cols: 4, CellW: 36}},
		{"80 wide", 80, 13, 4, TierGrid{Cards: 3, TileW: 26, Top: 6, Lines: 7, Cols: 2, CellW: 39}},
		{"narrower than a row", 20, 13, 1, TierGrid{Cards: 1, TileW: 20, Top: 6, Lines: 7, Cols: 1, CellW: 20}},
		{"exactly one row cell", 30, 13, 0, TierGrid{Lines: 13, Cols: 1, CellW: 30}},
	} {
		if got := c.Tiers(tc.w, tc.h, tc.urgent); got != tc.want {
			t.Errorf("%s: Tiers(%d,%d,%d) = %+v, want %+v", tc.name, tc.w, tc.h, tc.urgent, got, tc.want)
		}
	}
}

func TestTiered(t *testing.T) {
	c := Default
	for _, tc := range []struct {
		w, h, n int
		want    bool
	}{
		{150, 13, 9, false}, // every card fits
		{150, 13, 12, true}, // second card row full
		{150, 13, 20, true}, // the bug: 15+ agents
		{150, 6, 20, false}, // no room for cards and rows: chips line
		{150, 2, 20, false}, // chips line
		{20, 13, 3, true},   // narrow pane, one card per row
		{150, 13, 0, false}, // nobody
	} {
		if got := c.Tiered(tc.w, tc.h, tc.n); got != tc.want {
			t.Errorf("Tiered(%d,%d,%d) = %v, want %v", tc.w, tc.h, tc.n, got, tc.want)
		}
	}
}
