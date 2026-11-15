package kagent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
)

// Kubectl reaches the cluster: fleet never reads the kubeconfig itself.
var Kubectl = companion.Register(companion.Spec{
	Name:    "kubectl",
	Feature: "agents in a Kubernetes cluster through kagent (the kagent profile)",
	Hint:    "install kubectl (kubernetes.io/docs/tasks/tools), or set --url on the kagent profile",
})

// Forward is a kubectl port-forward to kagent's controller Service.
type Forward struct {
	Context   string // kube context; "" = kubectl's current one
	Namespace string // where kagent is installed
	Service   string // the controller Service
	Port      int    // its API port (the A2A gateway)
	Timeout   time.Duration
}

var forwarding = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+)`)

// Start runs the port-forward until ctx ends and returns the local URL.
func (f Forward) Start(ctx context.Context) (string, error) {
	t, err := companion.Find(Kubectl)
	if err != nil {
		return "", fmt.Errorf("kagent: %w", err)
	}
	var args []string
	if f.Context != "" {
		args = append(args, "--context", f.Context)
	}
	args = append(args, "-n", f.Namespace, "port-forward", "--address", "127.0.0.1",
		"svc/"+f.Service, fmt.Sprintf(":%d", f.Port))
	argv := t.Argv(args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("kubectl port-forward: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("kubectl port-forward: %w", err)
	}
	port := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if m := forwarding.FindStringSubmatch(sc.Text()); m != nil {
				select {
				case port <- m[1]:
				default:
				}
			}
		}
		_, _ = io.Copy(io.Discard, out)
		close(port)
	}()
	timeout := f.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	select {
	case p, ok := <-port:
		if ok {
			go func() { _ = cmd.Wait() }()
			return "http://127.0.0.1:" + p, nil
		}
		_ = cmd.Wait()
		return "", fmt.Errorf("kubectl port-forward svc/%s: %s", f.Service, companion.FirstLine(stderr.String()))
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", fmt.Errorf("kubectl port-forward svc/%s: no port after %s", f.Service, timeout)
	}
}

// Repo is the origin URL (without credentials) and branch of dir.
func Repo(ctx context.Context, dir string) (origin, branch string) {
	run := func(args ...string) string {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	origin = run("remote", "get-url", "origin")
	if u, err := url.Parse(origin); err == nil && u.User != nil {
		u.User = nil
		origin = u.String()
	}
	return origin, run("branch", "--show-current")
}
