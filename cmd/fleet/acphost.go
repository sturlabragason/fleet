package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/logx"
)

const acpHostUsage = "usage: fleet acp-host <name> [--dir D] [--session ID] [--prompt-file F] [--copilot-events] -- <argv...>"

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
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	argv := fs.Args()
	if len(argv) == 0 {
		return fmt.Errorf("%s", acpHostUsage)
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
	return acphost.Run(ctx, acphost.Config{
		Home: logx.Home(), Name: name, Dir: *dir, Argv: argv, Env: os.Environ(),
		SessionID: *session, Prompt: prompt, OnPromptSent: sent, CopilotEvents: *copilotEvents,
		Out: os.Stdout, In: os.Stdin,
	})
}
