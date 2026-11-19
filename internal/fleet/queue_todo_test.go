package fleet

import (
	"slices"
	"testing"
)

func TestNextLines(t *testing.T) {
	got := nextLines("done: shipped\nnext: write the docs\n- next: add a test\nnext:\nrisks: none")
	if want := []string{"write the docs", "add a test"}; !slices.Equal(got, want) {
		t.Errorf("nextLines = %q, want %q", got, want)
	}
}
