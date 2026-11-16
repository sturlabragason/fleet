package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sturlabragason/fleet/internal/kagent"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/prefs"
)

const kagentUsage = "usage: fleet kagent acp [--context CTX] [--agent NS/NAME] [--namespace NS] [--service SVC] [--port N] [--url URL] [--user ID] [--github-secret NS/NAME [--github-token-cmd CMD | --github-token-file PATH]]"

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
	ghSecret := fs.String("github-secret", "", "keep a GitHub token in this Secret, namespace/name, for an EgressPolicy")
	ghCmd := fs.String("github-token-cmd", "", "command that prints a GitHub token (run with sh every 30 minutes)")
	ghFile := fs.String("github-token-file", filepath.Join(filepath.Dir(prefs.Path()), "github-token"), "file holding a GitHub token, when no --github-token-cmd")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return errors.New(kagentUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGTERM, os.Interrupt)
	defer stop()
	if *ghSecret != "" {
		if err := keepGitHubToken(ctx, *kctx, *ghSecret, *ghCmd, *ghFile); err != nil {
			return err
		}
	}
	b := &kagent.Bridge{
		Gateway:  kagent.Gateway{URL: *url, Agent: *agent, User: *user, Token: os.Getenv("KAGENT_TOKEN")},
		StateDir: filepath.Join(logx.Home(), "kagent"),
		Cluster:  *kctx,
		Repo:     kagent.Repo,
		Report:   kagentReport,
	}
	if *url == "" {
		b.Connect = kagent.Forward{Context: *kctx, Namespace: *ns, Service: *svc, Port: *port}.Start
	}
	return b.Serve(ctx, os.Stdin, os.Stdout)
}

// kagentReport files a cluster agent's report as the fleet agent whose pane
// runs this adapter; acp-host runs it in that pane, so TMUX_PANE names it.
func kagentReport(_ context.Context, state, headline string) error {
	if err := mail.CheckReport(headline); err != nil {
		return fmt.Errorf("kagent report: %w", err)
	}
	f, err := openFleet()
	if err != nil {
		return fmt.Errorf("kagent report: %w", err)
	}
	me, err := f.Me("")
	if err != nil {
		return fmt.Errorf("kagent report: %w", err)
	}
	if _, err := f.Report(me, state, headline); err != nil {
		return fmt.Errorf("kagent report: %w", err)
	}
	return nil
}

// keepGitHubToken keeps a GitHub token from cmd, else file, in the cluster
// Secret ns/name while the adapter runs (ADR-146).
func keepGitHubToken(ctx context.Context, kctx, secret, cmd, file string) error {
	ns, name, ok := strings.Cut(secret, "/")
	if !ok || ns == "" || name == "" {
		return fmt.Errorf("--github-secret %q: want namespace/name", secret)
	}
	get := kagent.TokenFile(file)
	if cmd != "" {
		get = kagent.TokenCmd(cmd)
	}
	s := kagent.GitHubSecret{Context: kctx, Namespace: ns, Name: name}
	if err := kagent.KeepToken(ctx, get, s.Write); err != nil {
		return fmt.Errorf("kagent: %w", err)
	}
	return nil
}
