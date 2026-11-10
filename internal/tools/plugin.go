package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// PluginVersion is the fleet plugin's format version, in its plugin.json.
// Bump it when the files change shape; WritePlugin rewrites any file whose
// content differs, so a fleet upgrade updates every agent's plugin on its
// next spawn or wake either way.
const PluginVersion = "1.0.0"

// PluginName is the plugin's name in plugin.json. Claude namespaces plugin
// MCP tools with it; Copilot names them by server, as before.
const PluginName = "fleet"

// pluginEvents are the hook events the plugin wires to `fleet hook`, in
// Claude Code's names. Copilot reads a Claude-format hooks.json and maps
// them to its own (Stop → agentStop, UserPromptSubmit → userPromptSubmitted).
var pluginEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification",
	"PermissionRequest", "Stop", "SubagentStart", "SubagentStop", "PreCompact", "SessionEnd"}

// PluginDir is agent name's fleet plugin dir under the fleet home.
func PluginDir(home, name string) string { return filepath.Join(home, "plugins", name) }

// Plugin describes one agent's fleet plugin (ADR-96).
type Plugin struct {
	Exe, Name string
	// MCP adds .mcp.json with the agent's fleet-peer server (Copilot's
	// schema, see PeerMCP).
	MCP bool
	// NoStatus makes the hooks keep subagents only: an ACP agent's host
	// owns its status file.
	NoStatus bool
}

// files are the plugin's files by path relative to its dir.
func (pl Plugin) files() map[string][]byte {
	js := func(v any) []byte {
		b, _ := json.MarshalIndent(v, "", "  ")
		return append(b, '\n')
	}
	hooks := map[string]any{}
	for _, e := range pluginEvents {
		cmd := Quote(pl.Exe) + " hook " + strings.ToLower(e) + " --agent " + Quote(pl.Name)
		if pl.NoStatus {
			cmd += " --no-status"
		}
		hooks[e] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}}}
	}
	out := map[string][]byte{
		filepath.Join(".claude-plugin", "plugin.json"): js(map[string]any{
			"name": PluginName, "version": PluginVersion,
			"description": "fleet status hooks and peer tools for agent " + pl.Name}),
		filepath.Join("hooks", "hooks.json"): js(map[string]any{"hooks": hooks}),
	}
	if pl.MCP {
		cp, _ := PeerMCP(pl.Exe, pl.Name)
		var doc any
		_ = json.Unmarshal([]byte(cp), &doc)
		out[".mcp.json"] = js(doc)
	}
	return out
}

// WritePlugin writes pl into dir. Only files whose content differs are
// written, each by an atomic rename, so a running agent never reads half a
// file and a second spawn with the same plugin changes nothing; a .mcp.json
// pl no longer wants is removed.
func WritePlugin(dir string, pl Plugin) error {
	files := pl.files()
	for rel, data := range files {
		path := filepath.Join(dir, rel)
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}
	if _, ok := files[".mcp.json"]; !ok {
		if err := os.Remove(filepath.Join(dir, ".mcp.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// UsesPlugin reports whether the profile loads the fleet plugin under the
// given driver: it has plugin args, and under acp either the tool serves ACP
// itself (it takes the args) or its adapter takes the plugin in _meta.
func (p Profile) UsesPlugin(acp bool) bool {
	if len(p.PluginArgs) == 0 {
		return false
	}
	return !acp || len(p.ACPCommand) == 0 || p.ACPMeta == "claude"
}

// SessionMeta is the session/new _meta an ACP adapter takes for v (ADR-96):
// for acp_meta "claude", _meta.claudeCode.options with the plugin dir and
// the settings document (the write fence). "" when there is nothing to send.
func (p Profile) SessionMeta(v Vars) string {
	if p.ACPMeta != "claude" || (v.Plugin == "" && v.Hooks == "") {
		return ""
	}
	opts := map[string]any{}
	if v.Plugin != "" {
		opts["plugins"] = []any{map[string]any{"type": "local", "path": v.Plugin}}
	}
	if v.Hooks != "" {
		opts["settings"] = json.RawMessage(v.Hooks)
	}
	b, _ := json.Marshal(map[string]any{"claudeCode": map[string]any{"options": opts}})
	return string(b)
}
