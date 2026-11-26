package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/sessions"
	"github.com/sturlabragason/fleet/internal/spend"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/todo"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/wall"
)

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func obj(required []string, props map[string]any) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func strList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

var confirmedProp = map[string]any{"type": "boolean", "description": "true only after the user explicitly said yes in chat"}

// modelParamDesc describes the spawn tools' model parameter; profilesTool
// is the tool that lists a profile's models.
func modelParamDesc(profilesTool string) string {
	return "a model id the profile's tool offers, in the tool's own ids (" + profilesTool + " with profile lists them, " +
		"e.g. gemini-4-hydrogen or claude-sonnet-5.5 for copilot, gemini-pro-orchestrator-neon or gemini-4-hydrogen-high for agy, sonnet for claude; " +
		"never gemini-3.8-flash or agy's flash tier). Checked against the tool's own list when it has an ACP server; a model it doesn't list is refused, " +
		"never swapped for another, and so is any model for a profile with no model flag (vibe). Default: the profile's (Opus for copilot and claude, " +
		"the tool's own for agy), at every effort. The model is kept across park, wake and resume."
}

// toolDefs are the admiral's tools. The built-in ones are all in this
// initializer, which runs before any init(), so an extension's Register sees
// every one of them; extension tools follow.
var toolDefs = slices.Concat(baseToolDefs, cliToolDefs, []tool{instructionsToolDef, routeToolDef}, admiralToolDefs, []tool{waitToolDef})

var baseToolDefs = []tool{
	{"fleet_status", "List all agents: name, team, profile, driver (acp or tmux), status, running subagents, on_wall, age, spend so far, what it waits for, and where it runs (a where column, local or cluster:<context>, once an agent runs off this machine). Ends with the fleet-wide spend. Start here.", obj(nil, map[string]any{})},
	{"fleet_peek", "Show the last N lines (default 20, max 80) of one agent's screen, without box borders and blank runs (raw=true: as captured). last_message=true returns the agent's last reply from its session log instead. summary=true returns about 5 lines from a cheap model (what it is doing, what it waits for, any error) and costs a model call, so prefer it over long screens and don't poll it. Peek only for detail; fleet_info has the facts.", obj([]string{"name"}, map[string]any{
		"name": str("agent name"), "lines": map[string]any{"type": "integer", "description": "lines to return (default 20, max 80)"},
		"raw":          map[string]any{"type": "boolean", "description": "screen as captured, with borders"},
		"last_message": map[string]any{"type": "boolean", "description": "the agent's last reply from its session log (Copilot, Claude, ACP), at most 4000 chars; other tools get the end of the screen, headed (from the screen)"},
		"summary":      map[string]any{"type": "boolean", "description": "about 5 lines from a cheap model instead of screen text (cached per screen)"}})},
	{"fleet_send", "Send an ordinary prompt or follow-up text (pastes, presses Enter, re-presses it if the paste stays unsubmitted). Target exactly one of name, names or team; several targets get one line each. keys presses allowed keys instead of Enter (text+keys: paste, no Enter, then keys); text or keys to an agent on a prompt need confirmed=true (Enter would answer it). Refused while the user is typing in that pane (a key press there in the last 30 s, an interactive agent whose pane they have open, or unsubmitted text in its input box): wait or ask the user; force=true types anyway. An agent closed in the last 24 h is refused with when it closed: resume=true resumes its last session with its card, team and owner, then sends text (queued over max_working). keys=[\"Enter\"] submits a pane showing \"unsubmitted input: [Paste …]\". Text to a working agent steers it now: the result says \"steered\" (added to its running turn, or for a tool that takes no input mid-turn, turn stopped and text sent with a note), \"queued\" (it gets it when the turn ends) or \"sent\" (it was idle). queue=true holds text for the end of the turn instead (an FYI or a next task). now=true: an ACP agent working with no progress and a queued prompt has its turn cancelled and the queue sent at once.", obj(nil, map[string]any{
		"name": str("agent name"), "names": strList("several agent names"), "team": str("send to every member of this team"),
		"text": str("text to send"), "keys": strList("key names: Enter, Escape, Up, Down, Left, Right, Tab, BTab, Space, BSpace, 1-9, y, n"),
		"confirmed": map[string]any{"type": "boolean", "description": "only when an agent is waiting on a prompt (text or keys would answer it), after the user said yes"},
		"force":     map[string]any{"type": "boolean", "description": "type even though the user is typing in that pane (it merges into their input and submits it); only after the user said so"},
		"resume":    map[string]any{"type": "boolean", "description": "name only, with text: the agent closed; resume its last session, then send"},
		"now":       map[string]any{"type": "boolean", "description": "ACP agents, no keys: cancel the running turn (it looks stuck) and send the prompts it holds plus text at once; text may be empty"},
		"queue":     map[string]any{"type": "boolean", "description": "text only: an ACP agent that is working gets it when its turn ends instead of being steered now"}})},
	{"fleet_answer", "Answer an agent's prompt: answer y/n, option N of a numbered menu, or label for a menu whose options are not numbered on screen (fleet_status lists the options either way). Ask the user first; set confirmed=true only after an explicit yes, and pass expect = the question the user confirmed.", obj([]string{"name", "confirmed"}, map[string]any{
		"name": str("agent name"), "answer": map[string]any{"type": "string", "enum": []string{"y", "n"}, "description": "y = option 1, n = the first option starting with No; plain y/n when there is no menu"},
		"option": map[string]any{"type": "integer", "minimum": 1, "maximum": 9, "description": "menu option number; pass exactly one of answer, option or label"},
		"label":  str("text of the option to pick (part of it is enough, case-insensitive); for ❯ menus with no numbers on screen, e.g. \"Yes, I trust\""),
		"expect": str("the question the user confirmed (or part of it, or of the option label); refused if the prompt on screen no longer matches"), "confirmed": confirmedProp})},
	{"fleet_info", "Facts about one agent: profile, team, driver (acp with its ACP server, or tmux), where it runs (local or cluster:<context>), the fleet version that started it, status and age, last activity, dir, its task card (goal, done-check and its verdict, result file), git (worktree, branch, dirty files, merged into main, commits ahead), session id, context %, spend so far, mode, and the profile's effort prefix when it has one. Prefer it to peeking.", obj([]string{"name"}, map[string]any{
		"name": str("agent name")})},
	{"fleet_spawn", "Start a new agent with a tool profile in a directory, optionally with a first prompt. Never spawn in the home directory: pass the repo or project dir, not $HOME (fleet moves a home-directory spawn to a scratch dir of its own and says so). In a git repo the agent gets its own worktree, <repo>.wt/<name> on branch fleet/<name>, and works there; pass worktree=false to put it straight in dir (e.g. a read-only task, or work that must land in the user's checkout). goal, check and result make a task card: fleet runs check in the agent's dir when it reports done and tells you whether it passed, and the agent is told to write the result file. effort sizes the worker process the agent is given: pick it per task. interactive=true for an agent the user will talk to directly: it is never auto-closed and reports progress, not done, until the user ends the session. route=NAME fills dir, profile, effort and the rest from that route (fleet_route); explicit arguments win.", obj([]string{"name"}, map[string]any{
		"name": str("short unique agent name"), "route": str("a request route from the brief (fleet_route): fills every argument you leave out and wraps prompt in the route's template"), "profile": str("tool profile, see fleet_profiles; default [defaults] profile in config.toml, else claude-copilot"), "dir": str("working directory (absolute); required unless the route has one"), "prompt": str("optional first prompt"),
		"worktree":    map[string]any{"type": "boolean", "description": "give the agent its own git worktree (default: the user's config, normally true)"},
		"goal":        str("task card: the goal in one line (shown on the tile and in fleet_info)"),
		"check":       str("task card: done-check shell command, run in the agent's dir when it reports done, keep it cheap: a build plus vet, e.g. \"go build ./... && go vet ./internal/x/...\", or a check that CI passed on the branch head (tests run in CI, ADR-69, ADR-149)"),
		"check_exit":  map[string]any{"type": "integer", "description": "exit code the done-check must return to pass (default 0)"},
		"result":      str("task card: result file the agent must write, relative to its dir (default result.md)"),
		"may":         mayProp,
		"interactive": map[string]any{"type": "boolean", "description": "the user will talk to this agent directly in its pane: never auto-closed, and told to report progress, not done, until the user ends the session"},
		"leader":      map[string]any{"type": "boolean", "description": "make it a leader: it gets squad tools and may spawn a squad of its own, whose reports come to it instead of you. Use for a job you want planned, split and summarised by one agent."},
		"driver":      map[string]any{"type": "string", "enum": []string{"tmux", "acp"}, "description": "how fleet runs the agent: tmux (the tool's own screen) or acp (the tool's ACP server under fleet acp-host: status, questions and sends over the protocol, no screen reading). Default: the user's config, else acp for every profile that runs copilot or agy (claude-copilot too) and tmux for the rest. Claude runs over acp only when asked (this field or the profile's config), never from [defaults]; vibe refuses acp."},
		"effort":      map[string]any{"type": "string", "enum": []string{"light", "standard", "pro"}, "description": "how much process the agent is given. light: an errand or a quick read-only question (a few lines: stay in your dir, no secrets, report). standard: a contained change (plan, build and test, commit on its branch; no ADR, no review, no merge). pro: professional code work in this repo (the full playbook: worktree, failing test first, subagent review, pre-merge checklist, merge into local main). Default: pro with a worktree, light without one."},
		"model":       map[string]any{"type": "string", "description": modelParamDesc("fleet_profiles")}})},
	{"fleet_sessions", "Find copilot/claude sessions the user started outside fleet, newest first: id | age | profile | dir | title | flags. Use before fleet_adopt, with the user's words as the query; if more than one fits, ask which. Never guess an id.", obj(nil, map[string]any{
		"query": str("optional words; every word must appear in title, dir, branch or id"), "limit": map[string]any{"type": "integer", "description": "max sessions (default 15)"}})},
	{"fleet_adopt", "Resume an existing session the user started outside fleet as a fleet agent. Pass session_id (full id or unique 8+ char prefix) or query (words that match exactly one session), not both. Refuses if the session is missing or still open elsewhere.", obj(nil, map[string]any{
		"session_id": str("session id from fleet_sessions"), "query": str("words matching exactly one session"), "profile": str("optional tool profile; default the suggested one"),
		"dir": str("optional working directory (absolute); default from the session"), "name": str("optional agent name; default base name of dir"),
		"driver": map[string]any{"type": "string", "enum": []string{"tmux", "acp"}, "description": "as for fleet_spawn: acp loads the session in the tool's ACP server, tmux resumes it in the tool's own screen. Default as for fleet_spawn."}})},
	{"fleet_profiles", "List tool profiles that fleet_spawn accepts, with their model support; with profile, the models that profile's tool offers for fleet_spawn's model.", obj(nil, map[string]any{"profile": str("optional profile: list the models its tool offers (fleet starts the tool's ACP server once, no prompt, no tokens; cached 10 minutes). Without it: every profile, whether it can select a model, and its default.")})},
	{"fleet_close", "Stop an agent and remove it from the board. Ask the user first; set confirmed=true only after an explicit yes. The reply notes uncommitted or unmerged work in its dir. Closing a leader leaves its squad running and hands it back to you; squad=true closes its agents too.", obj([]string{"name", "confirmed"}, map[string]any{
		"name": str("agent name"), "confirmed": confirmedProp,
		"squad": map[string]any{"type": "boolean", "description": "for a leader: close its squad agents with it"}})},
	{"fleet_park", "Free RAM without losing work: park stops an idle agent's process but keeps it on the board with its session id; wake resumes that same session in the same directory. Only an idle agent can be parked - working, waiting, prompted and pinned agents, and one with unsubmitted text in its input box, are refused. The memory guard parks the longest-idle agent by itself when free RAM runs low and reports it here: wake those when there is room. A wake refused for memory: park another, force=true only if the user insists.", obj([]string{"name"}, map[string]any{
		"name": str("agent name"), "action": map[string]any{"type": "string", "enum": []string{"park", "wake"}, "description": "park (default) or wake"},
		"force": map[string]any{"type": "boolean", "description": "wake even while free memory is still under the guard's floor"}})},
	{"fleet_wall", "Change the live wall: show or hide an agent pane, set the layout preset (1,2,4,6,9,m), hide_all to take every agent pane off the wall (agents keep running, the admiral stays), restore to put back the panes the last hide_all took off, or admiral_show/admiral_hide to put a non-main admiral's pane under main or take it out of the column (its process keeps running). \"wall full: …\" is a no-op: offer to hide a named pane, don't retry. Presenting (ADR-200): present_on when the user says they are sharing their screen (no agent can open the browser), present_off when done, present for the state and, once not presenting, the blocked URLs to hand to the user.", obj([]string{"action"}, map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"show", "hide", "preset", "hide_all", "restore", "admiral_show", "admiral_hide", "present", "present_on", "present_off"}}, "name": str("agent name for show/hide, admiral name for admiral_show/admiral_hide"), "preset": str("1, 2, 4, 6, 9 or m")})},
	{"fleet_team", "Teams: named, coloured groups of agents (one team per agent). list; create (team, optional color); delete; add/remove (team, agents; add moves agents from another team and creates the team if missing); color (team, color); send (team, text) to every running member (members on a prompt are skipped unless confirmed=true); show/hide (team) on the wall; close (team) every member - ask the user first, confirmed=true only after an explicit yes.", obj([]string{"action"}, map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"list", "create", "delete", "add", "remove", "color", "send", "show", "hide", "close"}},
		"team":   str("team name"), "agents": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "agent names for add/remove"},
		"color": str("team colour name of the theme (lane: purple, teal, cyan, lilac, aqua, slate) or #rrggbb"), "text": str("text for send"), "confirmed": confirmedProp})},
	{"fleet_note", "Save one line to the decisions log: a user decision, preference or promise, or a RAID entry starting risk:, assumption:, issue: or decision:. Survives an admiral reset. pin=true for a standing user rule: fleet_brief always shows pinned notes, not just the last 30. rule=true also adds it to every new spawn's playbook. open=promise|ask|request (or text starting \"promise:\", \"ask:\", \"request:\") also makes it an open task (if the build has a todo store), listed in fleet_brief until done=<id> closes it.", obj(nil, map[string]any{
		"text": str("the note, one line; with done, the outcome (optional)"), "agent": str("optional agent the note is about"),
		"open": map[string]any{"type": "string", "enum": []string{"promise", "ask", "request"}, "description": "also an open task: your promise, your ask waiting on the user, or the user's request or question in flight"},
		"done": str("id of an open task or queue item to close (the 8 characters in [ ])"),
		"ctx":  str("the user's context for the task; default: from the agent's dir, else the configured default"),
		"pin":  map[string]any{"type": "boolean", "description": "a standing rule, always shown in fleet_brief"},
		"rule": map[string]any{"type": "boolean", "description": "a rule every agent follows: added to the user's worker playbook layer (fleet rule)"}})},
	{"fleet_brief", "Start-of-session brief, kept under 12 KB: the last handoff, the pinned notes, unread report headlines, the agents, the request routes and the recent notes that fit. Each cut section names the tool with the rest. Call first in a fresh session, then fleet_status. file= reads instead of briefing: a tool reply your client saved to a file (\"saved to /…copilot-tool-output…\"; you have no other tool to read it) file=pinned for every pinned note, file=todo for every open item ranked, or file=rules for the standing user rules every worker gets, in 8 KB pages from offset (each page names the next offset), or only the lines matching query.", obj(nil, map[string]any{
		"file":   str("the saved tool reply's full path, or pinned, todo or rules"),
		"offset": map[string]any{"type": "integer", "description": "byte offset of the page to read (default 0)"},
		"query":  str("with file: only the lines containing this text, with their offsets")})},
	{"fleet_access", "List agent-to-agent access: the agent_mail policy, then every explicit pair with its state (pending, granted, denied), who asked, who decided and why. limit=N adds the last N messages between agents.", obj(nil, map[string]any{
		"limit": map[string]any{"type": "integer", "description": "also show the last N agent-to-agent messages"}})},
	{"fleet_access_decide", "Grant, deny or revoke access between two agents. Ask the user first; set confirmed=true only after an explicit yes.", obj([]string{"a", "b", "action", "confirmed"}, map[string]any{
		"a": str("agent name"), "b": str("agent name"), "action": map[string]any{"type": "string", "enum": []string{"grant", "deny", "revoke"}}, "confirmed": confirmedProp})},
	reportsToolDef,
	{"fleet_handoff", "Write a handoff and get replaced by a fresh admiral in a few seconds. Include open questions, promises and plans in flight, not agent statuses; put each open question, ask and promise in open_items too, so it becomes a task the next brief lists.", obj([]string{"summary"}, map[string]any{
		"summary":    str("what the next admiral must know"),
		"open_items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "one per item: \"request: <the user's question or request in flight>\", \"ask: <waiting on the user>\" or \"promise: <what you promised>\""}})},
	{"fleet_admiral_profile", "Switch the admiral (you) to another tool profile, e.g. claude-personal; same profile = restart and resume this chat. Only when the user asks. You are replaced in a few seconds; pass a summary of open questions, promises and plans in flight.", obj([]string{"profile"}, map[string]any{
		"profile": str("tool profile, see fleet_profiles"), "summary": str("optional handoff for the next admiral")})},
	adrToolDef,
}

type args struct {
	Name, Text, Answer, Profile, Dir, Prompt, Action, Preset, Agent, Query string
	// Summary is text for fleet_handoff and fleet_report, true/false for
	// fleet_peek: one JSON name, two kinds of argument.
	Summary                                 summaryArg
	Expect                                  string
	To, From, Reason, Decision, A, B, State string
	// Note is fleet_handover's word to the receiver (ADR-185).
	Note string
	// Label picks a menu option by its text, for menus whose options are
	// not numbered on screen (fleet_answer).
	Label                string
	SessionID            string `json:"session_id"`
	Lines, Limit, Option int
	Number               int
	// File and Offset are fleet_brief's reader of a saved tool reply.
	File                       string
	Offset                     int
	Confirmed, All, Raw, Force bool
	// Leader makes a fleet_spawn a squad leader; Squad closes a leader's
	// squad with it (ADR-53).
	Leader, Squad bool
	Pin           bool
	// Rule makes a fleet_note a standing rule in every spawn (ADR-131).
	Rule bool
	// Resume brings a closed agent back for a fleet_send (ADR-95).
	Resume bool
	// Now makes a fleet_send to an ACP agent cancel its turn and send the
	// held prompts at once (ADR-152).
	Now bool
	// Queue makes a fleet_send hold text for the end of the running turn
	// instead of steering it (ADR-158).
	Queue bool
	// Interactive marks a fleet_spawn the user talks to directly (ADR-61).
	Interactive         bool
	LastMessage         bool `json:"last_message"`
	Team, Color         string
	Agents, Names, Keys []string
	// Worktree is fleet_spawn's opt-out; nil means "whatever config says".
	Worktree *bool
	// Effort is fleet_spawn's worker playbook level (ADR-57); "" is the
	// default for the spawn.
	Effort string
	// Model is fleet_spawn's and fleet_squad_spawn's model (ADR-100); ""
	// lets the effort pick.
	Model string
	// Driver is fleet_spawn's and fleet_squad_spawn's driver, tmux or acp
	// (ADR-74); "" is the config's.
	Driver string
	// Task card arguments of fleet_spawn (ADR-43).
	Goal, Check, Result string
	CheckExit           *int `json:"check_exit"`
	May                 []string
	// TimeoutS is fleet_wait's timeout in seconds (ADR-72).
	TimeoutS int `json:"timeout_s"`
	// CLI-backed tools (ADR-86): fleet_takeover's acp, fleet_touching's
	// files and graph, fleet_worktrees' and fleet_branches' prune.
	ACP bool `json:"acp"`
	// Client is fleet_admiral_add's MCP client for an external admiral
	// (ADR-159).
	Client string
	Files  []string
	// Graph is fleet_touching's graph; nil lets fleet pick (ADR-165).
	Graph *bool
	Prune bool
	// Days is fleet_recall's window (ADR-119).
	Days int
	// Route is fleet_spawn's request route; Fields are fleet_route set's
	// changes (ADR-120).
	Route  string
	Fields map[string]any
	// interactiveSet: the call passed interactive, so a route's doesn't apply.
	interactiveSet bool
	// NeedsAction is agent fleet_msg_send's wake switch; nil is the default,
	// fleet.NeedsAction (ADR-92).
	NeedsAction *bool `json:"needs_action"`
	// Fields of extra tools (ext.go).
	Peer, Thread, Kind string
	// Open, Done and Ctx make a fleet_note an admiral task in the todo store;
	// OpenItems are fleet_handoff's (ADR-164).
	Open, Done, Ctx string
	OpenItems       []string `json:"open_items"`
}

// identityBound are the admiral tools that change something or read the
// caller's own mail, reports or notes: refused when the admiral is unknown.
var identityBound = map[string]bool{
	"fleet_send": true, "fleet_answer": true, "fleet_spawn": true, "fleet_adopt": true,
	"fleet_close": true, "fleet_park": true, "fleet_team": true, "fleet_wall": true,
	"fleet_adr": true, "fleet_note": true, "fleet_reports": true, "fleet_handoff": true,
	"fleet_admiral_profile": true, "fleet_access_decide": true, "fleet_handover": true, "fleet_handover_request": true,
	"fleet_admirals": true, "fleet_msg_send": true, "fleet_inbox": true,
	"fleet_access_request": true, "fleet_access_respond": true, "fleet_access_revoke": true,
	"fleet_takeover": true, "fleet_recall": true, "fleet_route": true, "fleet_worktrees": true, "fleet_branches": true, "fleet_admiral_add": true,
	"fleet_admiral_close": true,
}

func (s *Server) call(name string, raw json.RawMessage) (string, error) {
	var a args
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	if s.Admiral == Unknown && identityBound[name] {
		return "", fmt.Errorf("admiral identity unknown: this MCP server could not tell which admiral it serves, so it refuses changes. Restart the admiral with fleet admiral restart --name <name>.")
	}
	if err := extGate(s, name, a); err != nil {
		return "", err
	}
	if out, ok, err := extCall(s, name, a); ok {
		return out, err
	}
	if h := toolHandlers[name]; h != nil {
		return h(s, name, a, raw)
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// toolHandler answers one tool call; name and raw are there for the few
// handlers that need them.
type toolHandler func(s *Server, name string, a args, raw json.RawMessage) (string, error)

// on adapts a handler that needs only the arguments.
func on(h func(*Server, args) (string, error)) toolHandler {
	return func(s *Server, _ string, a args, _ json.RawMessage) (string, error) { return h(s, a) }
}

var toolHandlers = map[string]toolHandler{
	"fleet_status":          on((*Server).statusCall),
	"fleet_peek":            on((*Server).peek),
	"fleet_send":            on((*Server).send),
	"fleet_answer":          on((*Server).answer),
	"fleet_info":            on((*Server).info),
	"fleet_spawn":           (*Server).spawnCall,
	"fleet_sessions":        on(func(s *Server, a args) (string, error) { return s.sessions(a.Query, a.Limit) }),
	"fleet_adopt":           on((*Server).adoptCall),
	"fleet_profiles":        on(func(s *Server, a args) (string, error) { return s.profiles(a.Profile) }),
	"fleet_close":           on((*Server).close),
	"fleet_park":            on((*Server).parkCall),
	"fleet_wall":            on((*Server).wallCall),
	"fleet_team":            on((*Server).team),
	"fleet_adr":             on(func(s *Server, a args) (string, error) { return s.adrCmd(a.Action, a.Agent, a.Dir, a.Number) }),
	"fleet_note":            on((*Server).noteCall),
	"fleet_route":           on((*Server).route),
	"fleet_brief":           on((*Server).briefCall),
	"fleet_admiral_profile": on(func(s *Server, a args) (string, error) { return s.admiralProfile(a.Profile, a.Summary.Text) }),
	"fleet_reports":         on(func(s *Server, a args) (string, error) { return s.reports(a.Name, a.All, a.Limit) }),
	"fleet_wait":            on(func(s *Server, a args) (string, error) { return s.wait(s.box(), waitReadAdmiral, a, nil) }),
	"fleet_access":          on((*Server).accessCall),
	"fleet_access_decide":   on((*Server).accessDecideCall),
	"fleet_handoff":         on((*Server).handoffCall),
}

func init() {
	for _, name := range []string{"fleet_handover", "fleet_handover_request", "fleet_admirals", "fleet_msg_send", "fleet_inbox", "fleet_access_request", "fleet_access_respond", "fleet_access_revoke"} {
		toolHandlers[name] = func(s *Server, name string, a args, _ json.RawMessage) (string, error) { return s.admiralCall(name, a) }
	}
	for _, name := range []string{"fleet_takeover", "fleet_touching", "fleet_worktrees", "fleet_branches", "fleet_admiral_add", "fleet_admiral_close", "fleet_changes", "fleet_recall"} {
		toolHandlers[name] = func(s *Server, name string, a args, _ json.RawMessage) (string, error) { return s.cliCall(name, a) }
	}
}

// need is the "<field> is required" check of the tool handlers.
func need(v, field string) error {
	if v == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

func (s *Server) statusCall(args) (string, error) {
	st, err := s.status()
	if off, since := s.F.Offline(); off {
		st = fleet.OfflineNote(since) + "\n" + st
	}
	return s.admiralHeader() + "\n" + st, err
}

func (s *Server) briefCall(a args) (string, error) {
	if a.File != "" {
		return s.readFile(a)
	}
	br, err := s.brief()
	return s.admiralHeader() + "\n" + br, err
}

func (s *Server) spawnCall(_ string, a args, raw json.RawMessage) (string, error) {
	var set struct{ Interactive *bool }
	_ = json.Unmarshal(raw, &set)
	a.interactiveSet = set.Interactive != nil
	via, err := s.applyRoute(&a)
	if err != nil {
		return "", err
	}
	for f, v := range map[string]string{"name": a.Name, "dir": a.Dir} {
		if err := need(v, f); err != nil {
			return "", err
		}
	}
	prof, err := s.spawnProfile(a.Profile, "fleet_profiles")
	if err != nil {
		return "", err
	}
	ag, err := s.F.SpawnWith(fleet.SpawnOpts{Worktree: a.Worktree, Card: a.card(), Role: fleet.Role{Leader: a.Leader, Interactive: a.Interactive, Admiral: s.Me()}, Effort: a.Effort, Driver: a.Driver, Model: a.Model}, a.Name, prof, a.Dir, a.Prompt)
	if err != nil {
		return "", err
	}
	if ag.Status == string(status.Queued) {
		return s.queuedReply(ag) + cardNote(ag), nil
	}
	if w := ag.Worktree; w != nil {
		return fmt.Sprintf("spawned %s (%s) in its own worktree %s on branch %s (of %s)", ag.Name, s.spawnLabel(ag), w.Path, w.Branch, w.Repo) + cardNote(ag) + leaderNote(ag) + via, nil
	}
	return fmt.Sprintf("spawned %s (%s) in %s%s", ag.Name, s.spawnLabel(ag), ag.Dir, s.dirNote(ag.Dir)) + cardNote(ag) + leaderNote(ag) + via, nil
}

func (s *Server) adoptCall(a args) (string, error) {
	o := fleet.AdoptOpts{Profile: a.Profile, Dir: a.Dir, Name: a.Name, Admiral: s.Me(), Driver: a.Driver}
	id, q := strings.TrimSpace(a.SessionID), strings.TrimSpace(a.Query)
	var res sessions.Session
	switch {
	case (id == "") == (q == ""):
		return "", fmt.Errorf("pass exactly one of session_id or query")
	case q != "":
		r, err := s.F.ResolveWords(a.Profile, strings.Fields(q))
		if err != nil {
			return "", err
		}
		res = r
	default:
		r, ok, err := s.F.ResolveID(a.Profile, id)
		if err != nil {
			return "", err
		}
		if !ok && a.Profile == "" {
			return "", fmt.Errorf("no session %s; try fleet_sessions", id)
		}
		res = r
		if !ok { // not in the store: Adopt reports it
			res = sessions.Session{ID: id, Profile: a.Profile}
		}
	}
	ag, err := s.F.AdoptResolved(o, res)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("adopted %s (%s) session %s in %s%s", ag.Name, ag.Profile, ag.SessionID, ag.Dir, s.dirNote(ag.Dir)), nil
}

func (s *Server) parkCall(a args) (string, error) {
	if err := need(a.Name, "name"); err != nil {
		return "", err
	}
	if err := s.scope(a.Name); err != nil {
		return "", err
	}
	switch a.Action {
	case "wake":
		ag, err := s.F.Wake(a.Name, a.Force)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("woke %s (%s), session %s, in %s", ag.Name, ag.Profile, ag.SessionID, ag.Dir), nil
	case "", "park":
		if err := s.F.Park(a.Name, ""); err != nil {
			return "", err
		}
		return fmt.Sprintf("parked %s; fleet_park name=%s action=wake resumes it", a.Name, a.Name), nil
	}
	return "", fmt.Errorf("action must be park or wake")
}

func (s *Server) wallCall(a args) (string, error) {
	switch a.Action {
	case "preset":
		return "wall preset " + a.Preset, s.W.SetPreset(a.Preset)
	case "show", "hide":
		p, err := s.F.Pane(a.Name)
		if err != nil {
			return "", err
		}
		if a.Action == "show" {
			ok, err := s.W.Show(p)
			if err != nil || !ok {
				return wall.FullMsg, err
			}
			return a.Name + " on wall", nil
		}
		return a.Name + " off wall", s.W.Hide(p)
	case "hide_all":
		n, err := s.W.HideAll()
		return wall.HiddenAllMsg(n), err
	case "restore":
		n, err := s.W.Restore()
		return wall.RestoredMsg(n), err
	case "admiral_hide":
		return wall.AdmiralMsg(a.Name, false), s.W.HideAdmiral(a.Name)
	case "present", "present_on", "present_off":
		return s.presentCall(a.Action)
	case "admiral_show":
		shown, err := s.W.ShowAdmiral(a.Name)
		if err != nil {
			return "", err
		}
		if !shown {
			return wall.NoRoomMsg, nil
		}
		return wall.AdmiralMsg(a.Name, true), nil
	}
	return "", fmt.Errorf("action must be show, hide, preset, hide_all, restore, admiral_show, admiral_hide, present, present_on or present_off")
}

func (s *Server) noteCall(a args) (string, error) {
	if a.Done != "" {
		return s.noteDone(a)
	}
	if err := need(strings.TrimSpace(a.Text), "text"); err != nil {
		return "", err
	}
	if err := todo.CheckOpen(a.Open); err != nil {
		return "", err
	}
	if err := checkRule(a.Rule, a.Text); err != nil {
		return "", err // refused before anything is logged
	}
	out, add := "noted", supervisor.AppendNoteBy
	if a.Pin {
		out, add = "pinned", supervisor.AppendPinnedBy
	}
	if err := add(s.F.Home, s.Me(), a.Agent, a.Text); err != nil {
		return out, err
	}
	out += s.noteTask(a)
	if !a.Rule {
		return out, nil
	}
	return ruleNote(out, s.F.Home, a.Text)
}

func (s *Server) accessCall(a args) (string, error) {
	out := PolicyLine(s.F.MailPolicy()) + "\n" + RenderPairs(s.F.Mail().All())
	if a.Limit > 0 {
		out = strings.TrimRight(out, "\n") + "\n\n" + RenderLog(s.F.Mail().Log(a.Limit))
	}
	return out, nil
}

func (s *Server) accessDecideCall(a args) (string, error) {
	if a.Action != "grant" && a.Action != "deny" && a.Action != "revoke" {
		return "", fmt.Errorf("action must be grant, deny or revoke")
	}
	if !a.Confirmed {
		return "", fmt.Errorf("not confirmed: ask the user, then call again with confirmed=true")
	}
	if err := s.scope(a.A, a.B); err != nil {
		return "", err
	}
	if err := s.F.AccessDecide(a.A, a.B, a.Action); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: %s and %s", a.Action, a.A, a.B), nil
}

func (s *Server) handoffCall(a args) (string, error) {
	if s.External {
		return "", errExternalReplace
	}
	if err := need(strings.TrimSpace(a.Summary.Text), "summary"); err != nil {
		return "", err
	}
	tasks := s.handoffTasks(a.OpenItems)
	if err := supervisor.WriteHandoffOf(s.F.Home, s.Me(), a.Summary.Text+tasks); err != nil {
		return "", err
	}
	if err := s.Reset(); err != nil {
		return "", fmt.Errorf("handoff saved, but reset failed: %w", err)
	}
	return "handoff saved; a fresh admiral takes over in a few seconds. Stop here.", nil
}

// queueNote adds the memory guard's reason to a "queued" reply when that is
// what held the spawn back, not the working cap.
func queueNote(ag *board.Agent) string {
	if ag.LastLine == "" {
		return ""
	}
	return " (" + ag.LastLine + ")"
}

// profiles lists the tool profiles a spawn accepts, one line each with
// its model support; with profile, the models its tool offers (ADR-112).
func (s *Server) profiles(profile string) (string, error) {
	if profile != "" {
		pm, err := s.F.ProfileModels(profile)
		if err != nil {
			return "", err
		}
		return modelList(pm), nil
	}
	var b strings.Builder
	for _, n := range tools.Names(s.F.Profiles) {
		p := s.F.Profiles[n]
		argv, _ := p.Argv(tools.Vars{Fleet: "fleet", Name: "<name>", PeerMCP: "<peer-mcp>", PeerMCPClaude: "<peer-mcp>"}, tools.Launch{})
		fmt.Fprintf(&b, "%s: %s", n, strings.Join(argv, " "))
		if pm, err := s.F.ModelSupport(n); err == nil {
			b.WriteString(modelSummary(pm))
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func modelSummary(pm fleet.ProfileModels) string {
	if !pm.Selectable {
		return " | model: fixed (no model flag)"
	}
	def := pm.Default
	if def == "" {
		def = "the tool's own"
	}
	return " | model: selectable, default " + def
}

func modelList(pm fleet.ProfileModels) string {
	var b strings.Builder
	b.WriteString(pm.Profile + modelSummary(pm) + "\n")
	if pm.Note != "" {
		b.WriteString(pm.Note + "\n")
	}
	if pm.Listed {
		fmt.Fprintf(&b, "the tool offers (its current: %s):\n", pm.Current)
	}
	for _, m := range pm.Models {
		b.WriteString(m.ModelID)
		if m.Name != "" && m.Name != m.ModelID {
			b.WriteString(" | " + m.Name)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (s *Server) status() (string, error) {
	as, err := s.F.Refresh()
	if err != nil {
		return "", err
	}
	if len(as) == 0 {
		return "no agents", nil
	}
	on := s.W.OnWall()
	var b strings.Builder
	teams := s.F.Teams.ByAgent()
	many := len(s.F.AdmiralNames()) > 1 // several admirals: say whose each agent is (§6.12)
	b.WriteString("name | team | squad | profile | driver | status | subagents | wall | age | spend | waiting_for | report")
	if many {
		b.WriteString(" | admiral")
	}
	remote := slices.ContainsFunc(as, func(a *board.Agent) bool { return a.Where != "" })
	if remote {
		b.WriteString(" | where") // local or cluster:<context>, once an agent runs off this machine (ADR-150)
	}
	b.WriteString("\n")
	var total spend.Spend
	queues := fleet.QueueCounts()
	for _, a := range as {
		wf := ""
		if a.Status == "waiting" || a.Status == "error" || a.Status == "stuck" {
			wf = s.waitingFor(a)
		}
		w := ""
		if on[a.Name] {
			w = "yes"
		}
		sub := ""
		if n := status.RunningCount(a.Subagents); n > 0 {
			sub = strconv.Itoa(n)
		}
		sp := ""
		if x, ok := s.F.SpendOf(a); ok {
			sp = x.Short()
			total.Add(x)
		}
		st := statusWord(a)
		if a.WithUser(time.Now()) {
			st += " (interactive)"
		}
		if a.Status != "working" {
			if q := s.F.Mail().UnreadFYI(a.Name); q > 0 {
				st += fmt.Sprintf(" (%d FYI queued)", q)
			}
		}
		if q := queues[a.Name]; q > 0 {
			st += fmt.Sprintf(" (queue %d)", q)
		}
		switch a.NoReportTag(time.Now()) {
		case "1":
			st += " (no report)"
		case "user":
			st += " (no report, the user's: don't send)"
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s", a.Name, teams[a.Name].Team, squadCell(s.F, a), a.Profile, a.DriverName(), st, sub, w, time.Since(a.StatusSince).Round(time.Second), sp, wf, reportCell(a))
		if many {
			fmt.Fprintf(&b, " | %s", s.F.OwnerOf(a.Name))
		}
		if remote {
			fmt.Fprintf(&b, " | %s", a.WhereName())
		}
		b.WriteString("\n")
	}
	if total.Any() {
		fmt.Fprintf(&b, "fleet spend so far: %s over %d model calls (%s); %s\n",
			total.Short(), total.Calls, spend.Tokens(total.Total())+" tokens", fleet.SpendNote)
	}
	return b.String(), nil
}

// sessions lists sessions to adopt, one line each.
func (s *Server) sessions(query string, limit int) (string, error) {
	if limit <= 0 {
		limit = 15
	}
	ss, err := s.F.Sessions(sessions.Opts{Words: strings.Fields(query), Limit: limit})
	if err != nil {
		return "", err
	}
	if len(ss) == 0 {
		return "no sessions match", nil
	}
	var b strings.Builder
	b.WriteString("id | age | profile | dir | title | flags\n")
	for _, x := range ss {
		var flags []string
		if x.InUse {
			flags = append(flags, "in use")
		}
		if x.OnBoard {
			flags = append(flags, "on board")
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s | %s | %s\n", x.ID, sessions.Age(time.Since(x.Updated)), x.Profile, x.Dir, sessions.OneLine(x.Title, 80), strings.Join(flags, ", "))
	}
	return b.String(), nil
}

// unpinned drops from notes the lines already shown as pinned.
func unpinned(notes, pinned []string) []string {
	if len(pinned) == 0 {
		return notes
	}
	pin := map[string]bool{}
	for _, p := range pinned {
		pin[p] = true
	}
	var out []string
	for _, n := range notes {
		if !pin[n] {
			out = append(out, n)
		}
	}
	return out
}

// summaryArg is the `summary` argument. fleet_handoff, fleet_admiral_profile
// and fleet_report pass text; fleet_peek passes true. One JSON name, two
// kinds of value, so neither tool makes the other's call fail to parse.
type summaryArg struct {
	Text string
	Flag bool
}

func (s *summaryArg) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "null":
		return nil
	case "true", "false":
		s.Flag = b[0] == 't'
		return nil
	}
	if err := json.Unmarshal(b, &s.Text); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(s.Text)) {
	case "true", "yes", "1":
		s.Flag = true
	}
	return nil
}

// dirNote tells the caller when fleet did not start the agent where it was
// asked to: a home-directory spawn goes to a scratch dir of its own (ADR-47).
func (s *Server) dirNote(dir string) string {
	if !s.F.InScratch(dir) {
		return ""
	}
	return " — " + fleet.RedirectNote()
}
