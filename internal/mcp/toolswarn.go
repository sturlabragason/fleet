package mcp

import "github.com/sturlabragason/fleet/internal/tools"

// toolsWarning tells the admiral that ~/.fleet/tools.toml does not parse:
// fleet keeps running on the built-in profiles, so nothing else would show it.
func (s *Server) toolsWarning() string {
	if s.F == nil {
		return ""
	}
	if err := tools.Broken(s.F.Home); err != nil {
		return "\n\n[" + err.Error() + ": its profiles are skipped and fleet runs on the built-in ones until it is fixed; fleet doctor shows it]"
	}
	return ""
}
