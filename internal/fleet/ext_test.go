package fleet

import "testing"

// withExts swaps the registry for one test.
func withExts(t *testing.T, es ...Extension) {
	old := exts
	exts = nil
	t.Cleanup(func() { exts = old })
	for _, e := range es {
		Register(e)
	}
}

func TestExtensionsCompose(t *testing.T) {
	var woke []string
	withExts(t,
		Extension{Name: "a", Wake: func(*Fleet) { woke = append(woke, "a") },
			AdmiralBadge: func(*Fleet) map[string]string { return map[string]string{"main": "A", "ops": "A"} }},
		Extension{Name: "b", Wake: func(*Fleet) { woke = append(woke, "b") },
			AdmiralBadge: func(*Fleet) map[string]string { return map[string]string{"main": "B"} }},
		Extension{Name: "c"},
	)
	f := &Fleet{}
	extWake(f)
	if len(woke) != 2 || woke[0] != "a" || woke[1] != "b" {
		t.Fatalf("wakes %v", woke)
	}
	if got := extAdmiralBadge(f); got["main"] != "A B" || got["ops"] != "A" || len(got) != 2 {
		t.Fatalf("badges %v", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a second registration of a name did not panic")
		}
	}()
	Register(Extension{Name: "a"})
}

