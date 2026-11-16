package kagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/ghapp"
)

// GitHubSecret is the cluster Secret a kagent EgressPolicy reads to add a
// GitHub credential on the way out (ADR-145 g). Fleet writes one key,
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

// KeepToken mints a token and writes it now, failing if either fails, then
// mints a new one in the background once the last is ten minutes from expiry,
// until ctx ends. It checks each minute against the wall clock, since a
// monotonic timer stops while the machine sleeps; a failed refresh is logged
// and retried at the next check.
func KeepToken(ctx context.Context, mint func(context.Context) (ghapp.Token, error), write func(context.Context, string) error) error {
	exp, err := refreshToken(ctx, mint, write)
	if err != nil {
		return err
	}
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			if time.Until(exp) > 10*time.Minute {
				continue
			}
			e, err := refreshToken(ctx, mint, write)
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("github token refresh", "component", "kagent", "err", err)
				}
				continue
			}
			exp = e
		}
	}()
	return nil
}

// refreshToken mints and writes one token and returns its expiry, with no
// monotonic reading so comparisons use the wall clock.
func refreshToken(ctx context.Context, mint func(context.Context) (ghapp.Token, error), write func(context.Context, string) error) (time.Time, error) {
	t, err := mint(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if err := write(ctx, t.Value); err != nil {
		return time.Time{}, err
	}
	return t.Expires.Round(0), nil
}
