package pages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sturlabragason/fleet/internal/companion"
)

// Ask is one question typed into a private page's Ask box (ADR-209).
type Ask struct {
	Page     string   `json:"page"`
	URL      string   `json:"url"`
	ID       string   `json:"id"`
	T        float64  `json:"t"`
	Question string   `json:"question"`
	Ctx      string   `json:"ctx"`
	Taken    *float64 `json:"taken"`
}

// askCmd runs `artifact ask <args>`.
func askCmd(ctx context.Context, args ...string) ([]byte, error) {
	t, err := companion.Find(Spec)
	if err != nil {
		return nil, fmt.Errorf("find artifact: %w", err)
	}
	out, err := t.Run(ctx, companion.Opts{}, append([]string{"ask"}, args...)...)
	if err != nil {
		return out, fmt.Errorf("artifact ask %s: %w", args[0], err)
	}
	return out, nil
}

// OpenAsks is every question no agent took yet: `artifact ask list`, one
// JSON object a line.
func OpenAsks(ctx context.Context) ([]Ask, error) {
	out, err := askCmd(ctx, "list")
	if err != nil {
		return nil, err
	}
	var open []Ask
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if l == "" {
			continue
		}
		var a Ask
		if err := json.Unmarshal([]byte(l), &a); err != nil {
			return nil, fmt.Errorf("artifact ask list: unreadable line: %w", err)
		}
		if a.Taken == nil {
			open = append(open, a)
		}
	}
	return open, nil
}

// TakeAsk claims a question for answering; false when another caller has
// it (exit 3), so each question starts one agent.
func TakeAsk(ctx context.Context, page, id string) (bool, error) {
	_, err := askCmd(ctx, "take", page, id)
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && ee.ExitCode() == 3 {
		return false, nil
	}
	return err == nil, err
}

// ReplyAsk posts text as the answer; the page shows it.
func ReplyAsk(ctx context.Context, page, id, text string) error {
	_, err := askCmd(ctx, "reply", page, id, text)
	return err
}
