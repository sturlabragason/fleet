package mcp

import "strings"

// Extension is what a feature compiled in with a build tag adds to the MCP
// server (DESIGN §15 "Extensions", ADR-126). It calls Register from its own
// init(); a default build registers nothing.
type Extension struct {
	Name string
	// Tools are extra admiral tools, at most one per extension, grouped by
	// an `action` enum (ADR-126). ToolNames lists them; they sit outside the
	// built-in budget.
	Tools []tool
	// Call serves Tools; it is only called with one of their names.
	Call func(s *Server, name string, a args) (string, error)
	// IdentityBound are the tools (its own or not) refused while the admiral
	// is unknown.
	IdentityBound []string
	// Parity adds CLIParity entries for the extension's commands.
	Parity map[string]string
	// Instructions edits the admiral instructions, e.g. adds its tools' rows.
	Instructions func(text string) string
	// Gate may refuse any admiral tool call before it runs.
	Gate func(s *Server, name string, a args) error
	// OnCall runs in the background on each admiral tool call.
	OnCall func(s *Server)
	// Suffix is appended to the admiral's tool results, like the reports badge.
	Suffix func(s *Server) string
	// Brief is added to fleet_brief after the unread reports.
	Brief func(s *Server) string
}

var (
	exts     []Extension
	extTools = map[string]int{} // tool name -> index in exts of the extension serving it
)

// Register adds an extension: its tools join the admiral's, its parity
// entries join CLIParity and every hook of every extension runs.
// Call it from init(). A name, tool or command registered twice panics.
func Register(e Extension) {
	if e.Name == "" {
		panic("mcp.Register: extension has no name")
	}
	for _, x := range exts {
		if x.Name == e.Name {
			panic("mcp.Register: extension " + e.Name + " registered twice")
		}
	}
	if len(e.Tools) > 0 && e.Call == nil {
		panic("mcp.Register: extension " + e.Name + " has tools but no Call")
	}
	for _, t := range e.Tools {
		for _, d := range toolDefs {
			if d.Name == t.Name {
				panic("mcp.Register: tool " + t.Name + " already exists")
			}
		}
	}
	for cmd := range e.Parity {
		if _, ok := CLIParity[cmd]; ok {
			panic("mcp.Register: CLIParity already has " + cmd)
		}
	}
	exts = append(exts, e)
	for _, t := range e.Tools {
		toolDefs = append(toolDefs, t)
		extTools[t.Name] = len(exts) - 1
	}
	for _, n := range e.IdentityBound {
		identityBound[n] = true
	}
	for cmd, v := range e.Parity {
		CLIParity[cmd] = v
	}
	if e.Instructions != nil {
		Instructions = e.Instructions(Instructions)
	}
}

// extCall serves an extension's tool; ok false means no extension has it.
func extCall(s *Server, name string, a args) (out string, ok bool, err error) {
	i, ok := extTools[name]
	if !ok {
		return "", false, nil
	}
	out, err = exts[i].Call(s, name, a)
	return out, true, err
}

// extGate runs every extension's gate; the first refusal wins.
func extGate(s *Server, name string, a args) error {
	for _, e := range exts {
		if e.Gate != nil {
			if err := e.Gate(s, name, a); err != nil {
				return err
			}
		}
	}
	return nil
}

func extOnCall(s *Server) {
	for _, e := range exts {
		if e.OnCall != nil {
			go e.OnCall(s)
		}
	}
}

func extSuffix(s *Server) string {
	var b strings.Builder
	for _, e := range exts {
		if e.Suffix != nil {
			b.WriteString(e.Suffix(s))
		}
	}
	return b.String()
}

func extBrief(s *Server) string {
	var b strings.Builder
	for _, e := range exts {
		if e.Brief != nil {
			b.WriteString(e.Brief(s))
		}
	}
	return b.String()
}
