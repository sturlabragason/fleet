package fleet

// Hooks for optional features compiled in with a build tag (DESIGN §15,
// "Extensions"); nil in a default build.
var (
	// extWake runs at the end of DeliverWake.
	extWake func(f *Fleet)
	// extAdmiralBadge is extra border text per admiral name (pane option
	// @fleet_admiral_ext); nil or a missing name shows nothing.
	extAdmiralBadge func(f *Fleet) map[string]string
)
