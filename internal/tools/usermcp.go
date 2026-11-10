package tools

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"strings"
)

// UserMCPServers reads the MCP servers in a tool's user-global config file
// ({"mcpServers":{name:{...}}}). A missing or unreadable file has none.
func UserMCPServers(path string) map[string]json.RawMessage {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(expandPath(path, ""))
	if err != nil {
		return nil
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	return cfg.MCPServers
}

// UserMCPNames returns the sorted server names in servers, leaving out
// fleet's own ("fleet", "fleet-peer") and those in keep.
func UserMCPNames(servers map[string]json.RawMessage, keep []string) []string {
	var names []string
	for n := range servers {
		if n != "fleet" && n != "fleet-peer" && !slices.Contains(keep, n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// UserMCP returns v set to switch off the user's global MCP servers for
// profile p, except those named in keep (ADR-91). Every call then carries
// only fleet-peer, the repo's own servers and the kept ones, instead of
// paying for tools no worker uses.
func (v Vars) UserMCP(p Profile, keep []string) Vars {
	if len(p.UserMCPOff) > 0 {
		v.MCPOff = UserMCPNames(UserMCPServers(p.UserMCPConfig), keep)
	}
	return v
}

// userMCPOffArgs expands UserMCPOff for the servers in off.
func (p Profile) userMCPOffArgs(off []string) []string {
	if len(off) == 0 || len(p.UserMCPOff) == 0 {
		return nil
	}
	if !slices.ContainsFunc(p.UserMCPOff, func(a string) bool { return strings.Contains(a, "{server}") }) {
		return slices.Clone(p.UserMCPOff)
	}
	var out []string
	for _, n := range off {
		for _, a := range p.UserMCPOff {
			out = append(out, strings.ReplaceAll(a, "{server}", n))
		}
	}
	return out
}
