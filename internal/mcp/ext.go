package mcp

// Hooks for optional features compiled in with a build tag (DESIGN §15,
// "Extensions"); nil in a default build. Extra tools append to toolDefs,
// identityBound and CLIParity in their own init.
var (
	// extCall serves an extra admiral tool; ok false means not one of its tools.
	extCall func(s *Server, name string, a args) (out string, ok bool, err error)
	// extGate may refuse any admiral tool call before it runs.
	extGate func(s *Server, name string, a args) error
	// extOnCall runs in the background on each admiral tool call.
	extOnCall func(s *Server)
	// extSuffix is appended to the admiral's tool results, like the reports badge.
	extSuffix func(s *Server) string
	// extBrief is added to fleet_brief after the unread reports.
	extBrief func(s *Server) string
)

func callExt(f func(*Server) string, s *Server) string {
	if f == nil {
		return ""
	}
	return f(s)
}
