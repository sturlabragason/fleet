//go:build extexample

package keys

// The public example extension's overview key (DESIGN §15 "Extensions");
// internal/ui's example opens its view on it.
func init() {
	Register(Extension{Name: "example", Bindings: []Binding{{Group: Overview, Key: "e",
		Desc: "the example extension's view (build tag extexample)", Hints: map[Scope]string{ScopeOverview: "e example"}}}})
}
