package tools

import (
	"encoding/json"
	"strings"
)

// PiExtension is the file Pi loads from the plugin dir with -e (ADR-148).
const PiExtension = "fleet-state.ts"

// piExtension reports Pi's own turn events to `fleet hook` and gives the
// agent its fleet-peer MCP server for this process only, so neither Pi's
// mcp.json nor the project's is written (ADR-91). The event mapping follows
// herdr's Pi integration (Apache-2.0, github.com/herdrdev/herdr).
//
// The hook runs with stdin closed: `fleet hook` reads a payload from stdin
// to EOF, and pi.exec would leave a pipe open. Calls run one at a time so
// the state file sees them in order.
const piExtension = `// Written by fleet for agent __NAME_TEXT__ (ADR-148); rewritten on every spawn and wake.
// Reports Pi's turn events to fleet and gives the agent its fleet-peer MCP tools.
// Event mapping after herdr's Pi integration (Apache-2.0, github.com/herdrdev/herdr).
// @ts-nocheck
import { spawn } from "node:child_process";

const exe = __EXE__;
const agent = __NAME__;

let chain = Promise.resolve();
function hook(event) {
  chain = chain.then(() => new Promise((done) => {
    try {
      const p = spawn(exe, ["hook", event, "--agent", agent], { stdio: "ignore" });
      p.on("error", () => done());
      p.on("exit", () => done());
    } catch {
      done();
    }
  }));
}

export default function (pi) {
__MCP__  let tui = false;
  let active = false;
  pi.on("session_start", (_event, ctx) => {
    tui = ctx?.mode === "tui";
    if (!tui) return;
    active = ctx?.isIdle?.() === false;
    hook(active ? "agent_start" : "agent_settled");
  });
  pi.on("agent_start", () => {
    active = true;
    if (tui) hook("agent_start");
  });
  pi.on("agent_settled", (_event, ctx) => {
    if (ctx?.isIdle?.() === false) return;
    active = false;
    if (tui) hook("agent_settled");
  });
  pi.on("ui_prompt_start", () => {
    if (tui) hook("ui_prompt_start");
  });
  pi.on("ui_prompt_end", () => {
    if (tui) hook(active ? "agent_start" : "agent_settled");
  });
}
`

// piFile is the extension for agent name run by exe; with mcp it registers
// the agent's fleet-peer server.
func piFile(exe, name string, mcp bool) []byte {
	js := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	reg := ""
	if mcp {
		reg = `  pi.registerMcpServer("fleet-peer", { command: exe, args: ["mcp", "--agent", agent], exposure: "direct" });` + "\n"
	}
	return []byte(strings.NewReplacer("__NAME_TEXT__", name, "__EXE__", js(exe), "__NAME__", js(name), "__MCP__", reg).Replace(piExtension))
}
