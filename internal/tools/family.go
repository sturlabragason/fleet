package tools

// The tool families: which tool-private code a profile gets (ADR-126). A
// profile's family is its `family` key, else its patterns value, so a profile
// for a new tool that behaves like Claude sets family = "claude" and keeps
// its own status patterns.
const (
	FamilyCopilot = "copilot"
	FamilyClaude  = "claude"
	FamilyAgy     = "agy"
	FamilyVibe    = "vibe"
	FamilyFake    = "fake"
)

// Traits is the family-specific behaviour that is a yes or no. Behaviour
// with more to it (the admiral launch, the spend parser) switches on
// Family() at its one call site.
type Traits struct {
	// AdmiralMCP: the admiral gets the fleet MCP tools under this family.
	AdmiralMCP bool
	// SessionEvents: under acp the tool sends its own session events once
	// asked to, which tell a foreground turn from a subagent's.
	SessionEvents bool
	// ContextFill: fleet reads the agent's context fill from its session
	// files (supervisor.Fill).
	ContextFill bool
}

var families = map[string]Traits{
	FamilyCopilot: {AdmiralMCP: true, SessionEvents: true, ContextFill: true},
	FamilyClaude:  {AdmiralMCP: true},
}

// Family is the profile's tool family: its family key, else its patterns.
func (p Profile) Family() string {
	if p.FamilyKey != "" {
		return p.FamilyKey
	}
	return p.Patterns
}

// Traits are the family's traits; the zero value for a family with none.
func (p Profile) Traits() Traits { return families[p.Family()] }
