package burn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/prefs"
)

// DefaultNanoGPTUsageURL is the default NanoGPT subscription usage endpoint.
const DefaultNanoGPTUsageURL = "https://nano-gpt.com/api/subscription/v1/usage"

// NanoGPTReader reads subscription quota snapshots from the NanoGPT usage API.
type NanoGPTReader struct {
	HTTPClient *http.Client
	ModelsPath string
}

// Name returns the reader identifier.
func (NanoGPTReader) Name() string { return "nanogpt" }

// Read queries the NanoGPT usage endpoint and returns the parsed Quota snapshot.
func (r NanoGPTReader) Read(ctx context.Context, profile string, cfg prefs.BurnProfile, env map[string]string) Quota {
	now := time.Now().UTC()
	q := Quota{
		Profile:   profile,
		Reader:    r.Name(),
		FetchedAt: now,
	}

	token, err := r.resolveToken(ctx, cfg.TokenEnv, env)
	if err != nil {
		q.Available = false
		q.Error = err.Error()
		q.Hint = "set NANOGPT_API_KEY or configure token_env"
		return q
	}

	usageURL := strings.TrimSpace(cfg.UsageURL)
	if usageURL == "" {
		usageURL = DefaultNanoGPTUsageURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		q.Available = false
		q.Error = fmt.Errorf("build nanogpt request: %w", err).Error()
		return q
	}
	if strings.Contains(usageURL, "/management/") {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.Header.Set("x-api-key", token)
	}

	client := r.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		q.Available = false
		q.Error = fmt.Errorf("query nanogpt usage: %w", err).Error()
		return q
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		q.Available = false
		q.Error = fmt.Errorf("read nanogpt response: %w", err).Error()
		return q
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		q.Available = false
		q.Error = fmt.Sprintf("nanogpt usage API returned HTTP %d", resp.StatusCode)
		return q
	}

	parsed, err := ParseNanoGPTUsage(body, now)
	if err != nil {
		q.Available = false
		q.Error = err.Error()
		return q
	}
	parsed.Profile = profile
	parsed.Reader = r.Name()
	parsed.FetchedAt = now
	return parsed
}

func (r NanoGPTReader) resolveToken(ctx context.Context, tokenEnv string, env map[string]string) (string, error) {
	lookup := func(name string) string {
		if v := strings.TrimSpace(env[name]); v != "" {
			return v
		}
		return strings.TrimSpace(os.Getenv(name))
	}
	if tokenEnv != "" {
		if v := lookup(tokenEnv); v != "" {
			return v, nil
		}
	}
	for _, name := range []string{"NANOGPT_MANAGEMENT_TOKEN", "NANOGPT_API_KEY"} {
		if v := lookup(name); v != "" {
			return v, nil
		}
	}

	modelsPath := r.ModelsPath
	if modelsPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("nanogpt API token not found")
		}
		modelsPath = filepath.Join(home, ".pi", "agent", "models.json")
	}

	data, err := os.ReadFile(modelsPath)
	if err != nil {
		return "", errors.New("nanogpt API token not found")
	}

	var doc struct {
		Providers map[string]struct {
			APIKey string `json:"apiKey"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse models config: %w", err)
	}

	key := strings.TrimSpace(doc.Providers["nanogpt"].APIKey)
	if key == "" {
		return "", errors.New("nanogpt API token not found")
	}
	if strings.HasPrefix(key, "!") {
		cmdStr := strings.TrimSpace(key[1:])
		if cmdStr == "" {
			return "", errors.New("empty nanogpt token command")
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), mapToEnvSlice(env)...)
		}
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("run nanogpt token command: %w", err)
		}
		tok := strings.TrimSpace(string(out))
		if tok == "" {
			return "", errors.New("nanogpt token command returned empty output")
		}
		return tok, nil
	}
	if strings.HasPrefix(key, "$") {
		tok := lookup(strings.TrimSpace(key[1:]))
		if tok == "" {
			return "", errors.New("nanogpt API token env var is empty")
		}
		return tok, nil
	}
	return key, nil
}

type nanoGPTWindowPayload struct {
	Used        int64   `json:"used"`
	Remaining   int64   `json:"remaining"`
	PercentUsed float64 `json:"percentUsed"`
	ResetAt     int64   `json:"resetAt"`
}

type nanoGPTUsagePayload struct {
	Active bool `json:"active"`
	Limits struct {
		WeeklyInputTokens *int64 `json:"weeklyInputTokens"`
		DailyInputTokens  *int64 `json:"dailyInputTokens"`
	} `json:"limits"`
	AllowOverage      bool                  `json:"allowOverage"`
	PaygoOverflow     bool                  `json:"paygoOverflow"`
	WeeklyInputTokens *nanoGPTWindowPayload `json:"weeklyInputTokens"`
	DailyInputTokens  *nanoGPTWindowPayload `json:"dailyInputTokens"`
	Routing           struct {
		PaidOverageEnabled bool `json:"paidOverageEnabled"`
	} `json:"routing"`
}

// ParseNanoGPTUsage decodes the NanoGPT subscription usage JSON into a Quota.
func ParseNanoGPTUsage(data []byte, now time.Time) (Quota, error) {
	var payload nanoGPTUsagePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return Quota{}, fmt.Errorf("parse nanogpt usage json: %w", err)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	var windows []Window
	if payload.WeeklyInputTokens != nil {
		limit := int64(0)
		if payload.Limits.WeeklyInputTokens != nil {
			limit = *payload.Limits.WeeklyInputTokens
		}
		windows = append(windows, buildNanoGPTWindow("weekly", "weeklyInputTokens", limit, payload.WeeklyInputTokens))
	}
	if payload.DailyInputTokens != nil && payload.Limits.DailyInputTokens != nil && *payload.Limits.DailyInputTokens > 0 {
		windows = append(windows, buildNanoGPTWindow("daily", "dailyInputTokens", *payload.Limits.DailyInputTokens, payload.DailyInputTokens))
	}

	overageOn := payload.AllowOverage || payload.PaygoOverflow || payload.Routing.PaidOverageEnabled
	q := Quota{
		Reader:    "nanogpt",
		Available: payload.Active,
		OverageOn: overageOn,
		Windows:   windows,
		FetchedAt: now.UTC(),
	}
	if !payload.Active {
		q.Error = "subscription inactive"
	}
	return q, nil
}

func buildNanoGPTWindow(kind, label string, limit int64, p *nanoGPTWindowPayload) Window {
	var remainingPct float64
	switch {
	case limit > 0 && (p.Remaining > 0 || p.Used > 0):
		remainingPct = float64(p.Remaining) / float64(limit) * 100
	case p.PercentUsed > 0:
		if p.PercentUsed <= 1 {
			remainingPct = (1 - p.PercentUsed) * 100
		} else {
			remainingPct = 100 - p.PercentUsed
		}
	case limit > 0:
		remainingPct = 100
	}
	if remainingPct < 0 {
		remainingPct = 0
	}
	if remainingPct > 100 {
		remainingPct = 100
	}

	var resetAt time.Time
	if p.ResetAt > 0 {
		if p.ResetAt < 1_000_000_000_000 {
			resetAt = time.Unix(p.ResetAt, 0).UTC()
		} else {
			resetAt = time.UnixMilli(p.ResetAt).UTC()
		}
	}

	return Window{
		Group:           "nanogpt",
		Kind:            kind,
		Label:           label,
		RemainingPct:    remainingPct,
		UsedTokens:      p.Used,
		RemainingTokens: p.Remaining,
		LimitTokens:     limit,
		ResetAt:         resetAt,
	}
}
