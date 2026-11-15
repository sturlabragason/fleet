package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sturlabragason/fleet/internal/kagent"
	"github.com/sturlabragason/fleet/internal/logx"
)

const kagentUsage = "usage: fleet kagent acp [--context CTX] [--agent NS/NAME] [--namespace NS] [--service SVC] [--port N] [--url URL] [--user ID]"

// cmdKagent is the ACP adapter the kagent profile runs under fleet
// acp-host (DESIGN §7d, ADR-145); it is not meant to be run by hand.
func cmdKagent(args []string) error {
	if len(args) == 0 || args[0] != "acp" {
		return errors.New(kagentUsage)
	}
	fs := flag.NewFlagSet("kagent acp", flag.ContinueOnError)
	kctx := fs.String("context", "", "kube context of the cluster (default: kubectl's current context)")
	agent := fs.String("agent", "kagent/claude", "the kagent Agent, namespace/name")
	ns := fs.String("namespace", "kagent", "namespace kagent is installed in")
	svc := fs.String("service", "kagent-controller", "kagent's controller Service")
	port := fs.Int("port", 8083, "the controller's API port (the A2A gateway)")
	url := fs.String("url", "", "the gateway's URL; skips kubectl port-forward")
	user := fs.String("user", "fleet", "X-User-Id for kagent's header authentication")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return errors.New(kagentUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGTERM, os.Interrupt)
	defer stop()
	b := &kagent.Bridge{
		Gateway:  kagent.Gateway{URL: *url, Agent: *agent, User: *user, Token: os.Getenv("KAGENT_TOKEN")},
		StateDir: filepath.Join(logx.Home(), "kagent"),
		Cluster:  *kctx,
		Repo:     kagent.Repo,
	}
	if *url == "" {
		b.Connect = kagent.Forward{Context: *kctx, Namespace: *ns, Service: *svc, Port: *port}.Start
	}
	return b.Serve(ctx, os.Stdin, os.Stdout)
}
