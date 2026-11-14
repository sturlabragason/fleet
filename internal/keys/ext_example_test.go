//go:build extexample

package keys

import (
	"strings"
	"testing"
)

func TestExampleExtBinding(t *testing.T) {
	var at, q int = -1, -1
	for i, b := range Bindings {
		if b.Group == Overview && b.Key == "e" {
			at = i
			if !b.Ext {
				t.Error("an extension's binding is not marked Ext")
			}
		}
		if b.Group == Overview && b.Key == "q" {
			q = i
		}
	}
	if at < 0 || at+1 != q {
		t.Fatalf("e at %d, q at %d: want e right before q", at, q)
	}
	if ExtOverviewHint != " · e example" || !strings.Contains(Hint(ScopeOverview, 300), "e example · ? all keys") {
		t.Fatalf("ExtOverviewHint %q, hint %q", ExtOverviewHint, Hint(ScopeOverview, 300))
	}
	if strings.Contains(markdown(true), "extexample") {
		t.Error("the public key docs list an extension's key")
	}
}
