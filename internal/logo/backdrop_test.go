package logo

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestParseBackdropLine(t *testing.T) {
	// Tile row: single word spaces stay untouched; 5-space interior run -> 3 glass cells; trailing -> crisp.
	l := ParseBackdropLine("│ a b     │  ", 16, false)
	if len(l.Spans) != 2 {
		t.Fatalf("tile spans = %+v, want 2", l.Spans)
	}
	if l.Spans[0] != (BackdropSpan{C0: 6, C1: 9, Glass: true}) {
		t.Errorf("interior span = %+v, want [6,9) glass", l.Spans[0])
	}
	if l.Spans[1] != (BackdropSpan{C0: 11, C1: 16, Glass: false}) {
		t.Errorf("trailing span = %+v, want [11,16) crisp", l.Spans[1])
	}

	// Styled background badge must NOT become transparent, and SGR foreground is restored after glass.
	styled := "\x1b[44m   \x1b[0m\x1b[32ma    b\x1b[0m"
	ls := ParseBackdropLine(styled, 12, false)
	if len(ls.Spans) != 2 {
		t.Fatalf("styled spans = %+v, want 2", ls.Spans)
	}
	if ls.Spans[0].C0 != 5 || ls.Spans[0].C1 != 7 || !ls.Spans[0].Glass || ls.Spans[0].Restore != "\x1b[32m" {
		t.Errorf("styled interior span = %+v, want [5,7) glass restore \\x1b[32m", ls.Spans[0])
	}

	// Reading panel (fullGlass): leading indent, interior gap, and trailing area all use blurred glass.
	lp := ParseBackdropLine("  key    val", 18, true)
	if len(lp.Spans) != 3 {
		t.Fatalf("panel spans = %+v, want 3", lp.Spans)
	}
	if !lp.Spans[0].Glass || lp.Spans[0].C0 != 0 || lp.Spans[0].C1 != 1 {
		t.Errorf("panel leading span = %+v, want [0,1) glass", lp.Spans[0])
	}
	if !lp.Spans[1].Glass || lp.Spans[1].C0 != 6 || lp.Spans[1].C1 != 8 {
		t.Errorf("panel interior span = %+v, want [6,8) glass", lp.Spans[1])
	}
	if !lp.Spans[2].Glass || lp.Spans[2].C0 != 13 || lp.Spans[2].C1 != 18 {
		t.Errorf("panel trailing span = %+v, want [13,18) glass", lp.Spans[2])
	}
}

func TestGlassEscapesFor(t *testing.T) {
	if got := GlassEscapesFor(colorprofile.Ascii); got != nil {
		t.Errorf("Ascii profile returned escapes %v, want nil", got)
	}
	esc := GlassEscapesFor(colorprofile.TrueColor)
	if len(esc) != BackdropCols {
		t.Fatalf("len(esc) = %d, want %d", len(esc), BackdropCols)
	}
	for i := CrispCols; i < BackdropCols; i++ {
		if esc[i] == "" {
			t.Errorf("glass escape %d is empty", i)
		}
	}
}
