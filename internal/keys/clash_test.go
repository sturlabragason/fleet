package keys

import (
	"slices"
	"testing"
)

// TestNoKeyConflicts fails when two bindings of one group name the same key:
// the overview would act on only one of them.
func TestNoKeyConflicts(t *testing.T) {
	for i, b := range Bindings {
		rest := Bindings[i+1:]
		for _, x := range rest {
			if x.Group != b.Group {
				continue
			}
			for _, k := range keyTokens(b.Key) {
				if slices.Contains(keyTokens(x.Key), k) {
					t.Errorf("%s: %q and %q both bind %s", b.Group, b.Key, x.Key, k)
				}
			}
		}
	}
}

func TestRegisterRefusesABoundKey(t *testing.T) {
	oldB, oldH, oldN := Bindings, ExtOverviewHint, extNames
	Bindings, ExtOverviewHint, extNames = slices.Clone(Bindings), "", map[string]bool{}
	t.Cleanup(func() { Bindings, ExtOverviewHint, extNames = oldB, oldH, oldN })
	for _, tc := range []struct {
		name  string
		b     Binding
		panic bool
	}{
		{"built-in key", Binding{Group: Overview, Key: "T"}, true},
		{"one of a pair", Binding{Group: Overview, Key: "+"}, true},
		{"one of a slash run", Binding{Group: Overview, Key: "9"}, true},
		{"modifier run", Binding{Group: Wall, Key: "Alt-4"}, true},
		{"chord is not its last key", Binding{Group: Wall, Key: "Alt-3"}, false},
		{"free key", Binding{Group: Overview, Key: "t"}, false},
		{"same key, other group", Binding{Group: Panes, Key: "T"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover() != nil; got != tc.panic {
					t.Fatalf("panicked %v, want %v", got, tc.panic)
				}
			}()
			Register(Extension{Name: tc.name, Bindings: []Binding{tc.b}})
		})
	}
}
