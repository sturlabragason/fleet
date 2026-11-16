package kagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
)

// GitHubSecret is the cluster Secret a kagent EgressPolicy reads to add a
// GitHub credential on the way out (ADR-146). Fleet writes one key,
// `authorization`: the Basic credential `x-access-token:<token>`, base64.
type GitHubSecret struct {
	Context   string // kube context; "" = kubectl's current one
	Namespace string
	Name      string
}

// manifest is the Secret as JSON for kubectl apply.
func (s GitHubSecret) manifest(token string) ([]byte, error) {
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	b, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "Opaque",
		"metadata": map[string]any{
			"name":      s.Name,
			"namespace": s.Namespace,
			"labels":    map[string]string{"app.kubernetes.io/managed-by": "fleet"},
		},
		"data": map[string]string{"authorization": base64.StdEncoding.EncodeToString([]byte(basic))},
	})
	if err != nil {
		return nil, fmt.Errorf("github secret: %w", err)
	}
	return b, nil
}

// Write applies the Secret with the token. The token goes on stdin, never
// argv, and server-side apply keeps it out of a last-applied annotation.
func (s GitHubSecret) Write(ctx context.Context, token string) error {
	m, err := s.manifest(token)
	if err != nil {
		return err
	}
	t, err := companion.Find(Kubectl)
	if err != nil {
		return fmt.Errorf("github secret: %w", err)
	}
	var args []string
	if s.Context != "" {
		args = append(args, "--context", s.Context)
	}
	args = append(args, "apply", "--server-side", "--force-conflicts", "--field-manager=fleet", "-f", "-")
	argv := t.Argv(args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(m)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("github secret %s/%s: %w: %s", s.Namespace, s.Name, err, companion.FirstLine(stderr.String()))
	}
	return nil
}

// TokenRefresh is how often KeepToken fetches the token again: half the
// hour a GitHub App installation token lasts.
const TokenRefresh = 30 * time.Minute

// TokenCmd returns a token source that runs cmd with sh and reads the token
// from its stdout, like git's credential helpers (ADR-146).
func TokenCmd(cmd string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		c := exec.CommandContext(ctx, "sh", "-c", cmd)
		var stderr strings.Builder
		c.Stderr = &stderr
		out, err := c.Output()
		if err != nil {
			return "", fmt.Errorf("github token command: %w: %s", err, companion.FirstLine(stderr.String()))
		}
		return checkToken(out, "github token command")
	}
}

// TokenFile returns a token source that reads path, owner-only, each time.
func TokenFile(path string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		fi, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("no GitHub token: %s does not exist and the profile sets no --github-token-cmd (see docs/kagent-github.md)", path)
		}
		if err != nil {
			return "", fmt.Errorf("github token file: %w", err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("github token file %s is readable by others; chmod 600 it", path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("github token file: %w", err)
		}
		return checkToken(b, "github token file "+path)
	}
}

// checkToken trims b and refuses an empty or multi-word token; the error
// never quotes it.
func checkToken(b []byte, from string) (string, error) {
	t := strings.TrimSpace(string(b))
	if t == "" || strings.ContainsAny(t, " \t\r\n") {
		return "", fmt.Errorf("%s: want one token on one line", from)
	}
	return t, nil
}

// KeepToken gets a token and writes it now, failing if either fails, then
// again every TokenRefresh in the background until ctx ends. It checks each
// minute against the wall clock, since a monotonic timer stops while the
// machine sleeps; a failed refresh is logged and retried at the next check.
func KeepToken(ctx context.Context, get func(context.Context) (string, error), write func(context.Context, string) error) error {
	if err := putToken(ctx, get, write); err != nil {
		return err
	}
	last := time.Now().Round(0)
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			if time.Since(last) < TokenRefresh {
				continue
			}
			if err := putToken(ctx, get, write); err != nil {
				if ctx.Err() == nil {
					slog.Warn("github token refresh", "component", "kagent", "err", err)
				}
				continue
			}
			last = time.Now().Round(0)
		}
	}()
	return nil
}

func putToken(ctx context.Context, get func(context.Context) (string, error), write func(context.Context, string) error) error {
	t, err := get(ctx)
	if err != nil {
		return err
	}
	return write(ctx, t)
}
