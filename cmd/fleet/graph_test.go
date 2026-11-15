package main

import "testing"

func TestGraphAfterMerge(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		found bool
		want  bool
	}{{"merge", true, true}, {"merge", false, false}, {"manual", true, false}, {"manual", false, false}} {
		if got := graphAfterMerge(tc.mode, tc.found); got != tc.want {
			t.Errorf("graphAfterMerge(%q, %v) = %v", tc.mode, tc.found, got)
		}
	}
}
