package board

// RunConfig fingerprints the configuration an agent was started with, so a
// run can be compared with runs under another configuration (ADR-170). It
// holds short hashes and names only, never instruction text. An empty field
// means that part was absent or unknown.
type RunConfig struct {
	// Playbook hashes the composed playbook text the first prompt carried:
	// every layer, the profile's note and a leader's orientation.
	Playbook string `json:"playbook,omitempty"`
	// Layers hashes each playbook layer on its own: "builtin:<level>",
	// "user" (<fleet home>/worker.md) and "repo" (.fleet/worker.md).
	Layers map[string]string `json:"layers,omitempty"`
	// AgentsMD hashes the repo's AGENTS.md, else its CLAUDE.md.
	AgentsMD string `json:"agents_md,omitempty"`
	// Route is the route's name and the hash of its saved settings,
	// "<name>@<hash>".
	Route string `json:"route,omitempty"`
}
