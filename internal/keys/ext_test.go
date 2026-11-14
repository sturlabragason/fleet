package keys

import (
	"slices"
	"testing"
)

func TestExtensionsCompose(t *testing.T) {
	oldB, oldH, oldN := Bindings, ExtOverviewHint, extNames
	Bindings, ExtOverviewHint, extNames = slices.Clone(Bindings), "", map[string]bool{}
	t.Cleanup(func() { Bindings, ExtOverviewHint, extNames = oldB, oldH, oldN })
	Register(Extension{Name: "a", Bindings: []Binding{{Group: Overview, Key: "1x", Hints: map[Scope]string{ScopeOverview: "1x a"}}}})
	Register(Extension{Name: "b", Bindings: []Binding{{Group: Overview, Key: "2x", Hints: map[Scope]string{ScopeOverview: "2x b"}}}})
	var order []string
	for _, b := range Bindings {
		if b.Group == Overview {
			order = append(order, b.Key)
		}
	}
	if n := len(order); n < 3 || order[n-3] != "1x" || order[n-2] != "2x" || order[n-1] != "q" {
		t.Fatalf("overview order ends %v", order[max(0, len(order)-3):])
	}
	if ExtOverviewHint != sep+"1x a"+sep+"2x b" {
		t.Fatalf("ExtOverviewHint %q", ExtOverviewHint)
	}
}
