package acphost

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sturlabragason/fleet/internal/acp"
)

// ParseMCP turns a Claude-style MCP config ({"mcpServers":{name:{command,
// args, env}}}) into the stdio servers a session takes (ADR-76). "" is none.
func ParseMCP(s string) ([]acp.MCPServer, error) {
	if s == "" {
		return nil, nil
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return nil, fmt.Errorf("mcp config: %w", err)
	}
	names := make([]string, 0, len(doc.MCPServers))
	for n := range doc.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []acp.MCPServer
	for _, n := range names {
		d := doc.MCPServers[n]
		if d.Command == "" {
			return nil, fmt.Errorf("mcp config: server %q has no command (only stdio servers)", n)
		}
		srv := acp.MCPServer{Name: n, Command: d.Command, Args: d.Args, Env: []acp.EnvVariable{}}
		if srv.Args == nil {
			srv.Args = []string{}
		}
		keys := make([]string, 0, len(d.Env))
		for k := range d.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			srv.Env = append(srv.Env, acp.EnvVariable{Name: k, Value: d.Env[k]})
		}
		out = append(out, srv)
	}
	return out, nil
}

// FilterEnv drops the named variables from env (KEY=VAL pairs).
func FilterEnv(env, drop []string) []string {
	if len(drop) == 0 {
		return env
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		keep := true
		for _, d := range drop {
			if k == d {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}
