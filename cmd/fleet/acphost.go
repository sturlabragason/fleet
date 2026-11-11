package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/logx"
	"golang.org/x/term"
)

const acpHostUsage = "usage: fleet acp-host <name> [--dir D] [--session ID] [--prompt-file F] [--copilot-events] [--mode M] [--mcp JSON] [--meta JSON] [--unset-env A,B] [--profile P] [--model M] -- <argv...>"

// cmdACPHost runs one ACP agent in this terminal (its tmux window) until it
// is stopped or its child exits (DESIGN §7d, ADR-74).
func cmdACPHost(args []string) error {
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		return fmt.Errorf("%s", acpHostUsage)
	}
	name := args[0]
	fs := flag.NewFlagSet("acp-host", flag.ContinueOnError)
	dir := fs.String("dir", "", "the agent's working dir")
	session := fs.String("session", "", "resume or load this session")
	promptFile := fs.String("prompt-file", "", "send this file's text as the first prompt (the file is removed once sent)")
	copilotEvents := fs.Bool("copilot-events", false, "opt in to Copilot's session events")
	mode := fs.String("mode", "", "session mode to set (session/set_mode) after the session starts")
	mcpJSON := fs.String("mcp", "", `MCP servers for the session, as {"mcpServers":{...}} with stdio servers`)
	metaJSON := fs.String("meta", "", "_meta object for session/new, load and resume (claude-agent-acp options)")
	unset := fs.String("unset-env", "", "comma-separated variables the agent must not inherit")
	profile := fs.String("profile", "", "the fleet profile, shown in the pane's header")
	model := fs.String("model", "", "the model the session must run, switched over ACP when the agent names another (ADR-112)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	argv := fs.Args()
	if len(argv) == 0 {
		return fmt.Errorf("%s", acpHostUsage)
	}
	mcp, err := acphost.ParseMCP(*mcpJSON)
	if err != nil {
		return err
	}
	var meta json.RawMessage
	if *metaJSON != "" {
		if !json.Valid([]byte(*metaJSON)) {
			return fmt.Errorf("acp-host: --meta is not JSON")
		}
		meta = json.RawMessage(*metaJSON)
	}
	var drop []string
	for _, k := range strings.Split(*unset, ",") {
		if k = strings.TrimSpace(k); k != "" {
			drop = append(drop, k)
		}
	}
	// A second host for the same agent must fail before it eats the first
	// prompt that belongs to the running one's spawn.
	if _, err := acphost.Call(logx.Home(), name, acphost.Request{Op: acphost.OpState}, 500*time.Millisecond); err == nil {
		return fmt.Errorf("acp-host: %s: a host is already running", name)
	}
	prompt := ""
	if *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			return err
		}
		prompt = string(b)
	}
	// The file goes only once its prompt is with the agent: a host that
	// fails to start leaves it for the next one.
	var sent func()
	if *promptFile != "" {
		sent = func() { _ = os.Remove(*promptFile) }
	}
	logx.Init()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGTERM, os.Interrupt)
	defer stop()
	cfg := acphost.Config{
		Home: logx.Home(), Name: name, Dir: *dir, Argv: argv, Env: acphost.FilterEnv(os.Environ(), drop),
		SessionID: *session, Prompt: prompt, OnPromptSent: sent, CopilotEvents: *copilotEvents,
		Mode: *mode, Model: *model, MCP: mcp, Meta: meta, Profile: *profile,
		Out: os.Stdout, In: os.Stdin,
	}
	// A terminal gets the input line: raw key presses, so Esc cancels and
	// Ctrl-C never stops the agent (ADR-82).
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		if old, err := term.MakeRaw(fd); err == nil {
			defer term.Restore(fd, old)
			os.Stdout.WriteString(acphost.ScreenOn + acphost.PasteOn + acphost.MouseOn)
			defer os.Stdout.WriteString(acphost.MouseOff + acphost.PasteOff + acphost.ScreenOff)
			cfg.Keys = true
			cfg.Color = acphost.ColorProfile(os.Environ())
			cfg.Size = func() (int, int) {
				w, h, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil {
					return 0, 0
				}
				return w, h
			}
			winch := make(chan os.Signal, 1)
			signal.Notify(winch, syscall.SIGWINCH)
			defer signal.Stop(winch)
			cfg.Resized = winch
			if exe, err := os.Executable(); err == nil {
				cfg.Takeover = func(say func(string)) error { return paneTakeover(exe, logx.Home(), name, say) }
			}
		}
	}
	return acphost.Run(ctx, cfg)
}

// paneTakeover runs `fleet takeover <name>` for Ctrl-T in the pane. The
// takeover kills this pane and every process under it, so it runs outside
// the pane's process tree: a shell starts it in the background in a new
// session and exits. Its output goes to a log; if the host is still alive
// when that log fills, the takeover failed and the pane shows why.
func paneTakeover(exe, home, name string, say func(string)) error {
	log := filepath.Join(acphost.Dir(home), name+".takeover.log")
	_ = os.Remove(log)
	cmd := exec.Command("/bin/sh", "-c", `"$0" takeover "$1" >"$2" 2>&1 </dev/null &`, exe, name, log)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// The user pressed the key, not the agent: without TMUX_PANE the CLI
	// does not take the call for one from this agent's pane (leaderScope).
	cmd.Env = acphost.FilterEnv(os.Environ(), []string{"TMUX_PANE"})
	if err := cmd.Run(); err != nil {
		return err
	}
	go func() {
		for end := time.Now().Add(time.Minute); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
			if b, err := os.ReadFile(log); err == nil && len(strings.TrimSpace(string(b))) > 0 {
				time.Sleep(250 * time.Millisecond) // let it finish writing
				b, _ = os.ReadFile(log)
				say("! " + strings.TrimSpace(string(b)))
				return
			}
		}
	}()
	return nil
}
