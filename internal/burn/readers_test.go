package burn

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/prefs"
)

const agyFixture = `Quota:
Gemini Models          Weekly Limit Remaining       95%   2026-11-28 12:24 CET
Gemini Models          Five Hour Limit Remaining    99%   2026-11-23 01:51 CET
Claude and GPT models  Weekly Limit Remaining       100%  2026-11-29 20:52 CET
Claude and GPT models  Five Hour Limit Remaining    100%  2026-11-23 01:52 CET
Gemini 4 Argon         Gemini 4 Argon Weekly Limit  100%  2026-11-29 20:52 CET
`

const agyRealFixture = "Gemini Models\tWeekly Limit Remaining\t100%\t2026-12-03T18:00:11Z\n" +
	"Gemini Models\tFive Hour Limit Remaining\t100%\t2026-11-26T23:00:11Z\n" +
	"Claude and GPT models\tWeekly Limit Remaining\t100%\t1970-01-01T00:00:00Z\n" +
	"Claude and GPT models\tFive Hour Limit Remaining\t100%\t1970-01-01T00:00:00Z\n"

func TestParseAgyQuota(t *testing.T) {
	t.Helper()
	now := time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    []Window
	}{
		{
			name:  "standard 5-window output",
			input: agyFixture,
			want: []Window{
				{Group: "Gemini Models", Kind: "weekly", Label: "Weekly Limit Remaining", RemainingPct: 95, ResetAt: time.Date(2026, 11, 28, 11, 24, 0, 0, time.UTC)},
				{Group: "Gemini Models", Kind: "5h", Label: "Five Hour Limit Remaining", RemainingPct: 99, ResetAt: time.Date(2026, 11, 23, 0, 51, 0, 0, time.UTC)},
				{Group: "Claude and GPT models", Kind: "weekly", Label: "Weekly Limit Remaining", RemainingPct: 100, ResetAt: time.Date(2026, 11, 29, 19, 52, 0, 0, time.UTC)},
				{Group: "Claude and GPT models", Kind: "5h", Label: "Five Hour Limit Remaining", RemainingPct: 100, ResetAt: time.Date(2026, 11, 23, 0, 52, 0, 0, time.UTC)},
				{Group: "Gemini 4 Argon", Kind: "weekly", Label: "Gemini 4 Argon Weekly Limit", RemainingPct: 100, ResetAt: time.Date(2026, 11, 29, 19, 52, 0, 0, time.UTC)},
			},
		},
		{
			name:  "other timezones and daily kind",
			input: "Model A  Daily Limit Remaining  42.5%  2026-11-23 10:00 EST\nModel B  Monthly Limit  80%  2026-12-01 00:00 CEST\n",
			want: []Window{
				{Group: "Model A", Kind: "daily", Label: "Daily Limit Remaining", RemainingPct: 42.5, ResetAt: time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)},
				{Group: "Model B", Kind: "other", Label: "Monthly Limit", RemainingPct: 80, ResetAt: time.Date(2026, 11, 30, 22, 0, 0, 0, time.UTC)},
			},
		},
		{
			// Captured from agy 2026-11-26: tab-separated, RFC3339, epoch for "no reset".
			name:  "real tab-separated output",
			input: agyRealFixture,
			want: []Window{
				{Group: "Gemini Models", Kind: "weekly", Label: "Weekly Limit Remaining", RemainingPct: 100, ResetAt: time.Date(2026, 12, 3, 18, 0, 11, 0, time.UTC)},
				{Group: "Gemini Models", Kind: "5h", Label: "Five Hour Limit Remaining", RemainingPct: 100, ResetAt: time.Date(2026, 11, 26, 23, 0, 11, 0, time.UTC)},
				{Group: "Claude and GPT models", Kind: "weekly", Label: "Weekly Limit Remaining", RemainingPct: 100},
				{Group: "Claude and GPT models", Kind: "5h", Label: "Five Hour Limit Remaining", RemainingPct: 100},
			},
		},
		{name: "empty output", input: "", wantErr: true},
		{name: "header only without percentages", input: "Quota:\nNo active limits\n", wantErr: true},
		{name: "malformed reset timestamp", input: "Gemini Models  Weekly Limit Remaining  95%  not-a-date\n", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAgyQuota(tc.input, now)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got windows=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d windows, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				assertWindowEqual(t, got[i], tc.want[i])
			}
		})
	}
}

const nanoGPTFixture = `{"active":true,"limits":{"weeklyInputTokens":60000000,"dailyInputTokens":null},"allowOverage":false,"paygoOverflow":false,"weeklyInputTokens":{"used":12094326,"remaining":47905674,"percentUsed":0.2015721,"resetAt":1795392000000},"dailyInputTokens":null,"routing":{"paidOverageEnabled":false}}`

func TestParseNanoGPTUsage(t *testing.T) {
	t.Helper()
	now := time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		input         string
		wantErr       bool
		wantAvail     bool
		wantOverage   bool
		wantErrorMsg  string
		wantWindows   int
		wantPctApprox float64
		wantResetAt   time.Time
	}{
		{
			name:          "live fixture",
			input:         nanoGPTFixture,
			wantAvail:     true,
			wantOverage:   false,
			wantWindows:   1,
			wantPctApprox: 79.84279,
			wantResetAt:   time.UnixMilli(1795392000000).UTC(),
		},
		{
			name:          "overage enabled and daily window",
			input:         `{"active":true,"limits":{"weeklyInputTokens":1000,"dailyInputTokens":200},"allowOverage":true,"paygoOverflow":false,"weeklyInputTokens":{"used":200,"remaining":800,"percentUsed":0.2,"resetAt":1795392000000},"dailyInputTokens":{"used":50,"remaining":150,"percentUsed":0.25,"resetAt":1795305600000},"routing":{"paidOverageEnabled":false}}`,
			wantAvail:     true,
			wantOverage:   true,
			wantWindows:   2,
			wantPctApprox: 80.0,
			wantResetAt:   time.UnixMilli(1795392000000).UTC(),
		},
		{
			name:          "routing paidOverageEnabled sets overage",
			input:         `{"active":true,"limits":{"weeklyInputTokens":1000},"allowOverage":false,"paygoOverflow":false,"weeklyInputTokens":{"used":0,"remaining":1000,"percentUsed":0,"resetAt":1795392000000},"routing":{"paidOverageEnabled":true}}`,
			wantAvail:     true,
			wantOverage:   true,
			wantWindows:   1,
			wantPctApprox: 100.0,
			wantResetAt:   time.UnixMilli(1795392000000).UTC(),
		},
		{
			name:         "inactive subscription",
			input:        `{"active":false,"limits":{"weeklyInputTokens":60000000},"allowOverage":false,"paygoOverflow":false,"routing":{"paidOverageEnabled":false}}`,
			wantAvail:    false,
			wantOverage:  false,
			wantErrorMsg: "subscription inactive",
			wantWindows:  0,
		},
		{name: "invalid json", input: `{bad`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseNanoGPTUsage([]byte(tc.input), now)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Available != tc.wantAvail || got.OverageOn != tc.wantOverage || got.Error != tc.wantErrorMsg {
				t.Fatalf("unexpected quota state: %+v", got)
			}
			if len(got.Windows) != tc.wantWindows {
				t.Fatalf("got %d windows, want %d", len(got.Windows), tc.wantWindows)
			}
			if tc.wantWindows > 0 {
				w := got.Windows[0]
				if w.Group != "nanogpt" || w.Kind != "weekly" {
					t.Fatalf("unexpected window header: %+v", w)
				}
				if math.Abs(w.RemainingPct-tc.wantPctApprox) > 0.01 {
					t.Fatalf("RemainingPct = %.5f, want ~%.5f", w.RemainingPct, tc.wantPctApprox)
				}
				if !w.ResetAt.Equal(tc.wantResetAt) {
					t.Fatalf("ResetAt = %v, want %v", w.ResetAt, tc.wantResetAt)
				}
			}
		})
	}
}

func TestNanoGPTReaderRead(t *testing.T) {
	t.Helper()

	tests := []struct {
		name           string
		usagePath      string
		tokenEnv       string
		envKey         string
		envVal         string
		modelsJSON     string
		statusCode     int
		body           string
		wantHeaderKey  string
		wantHeaderVal  string
		wantAvailable  bool
		wantPctApprox  float64
		wantErrContain string
	}{
		{
			name:          "subscription endpoint with NANOGPT_API_KEY",
			usagePath:     "/api/subscription/v1/usage",
			envKey:        "NANOGPT_API_KEY",
			envVal:        "test-key",
			statusCode:    http.StatusOK,
			body:          nanoGPTFixture,
			wantHeaderKey: "x-api-key",
			wantHeaderVal: "test-key",
			wantAvailable: true,
			wantPctApprox: 79.84,
		},
		{
			name:          "management endpoint with custom TokenEnv",
			usagePath:     "/api/management/v1/usage",
			tokenEnv:      "CUSTOM_NANO_TOKEN",
			envKey:        "CUSTOM_NANO_TOKEN",
			envVal:        "mgmt-secret",
			statusCode:    http.StatusOK,
			body:          nanoGPTFixture,
			wantHeaderKey: "Authorization",
			wantHeaderVal: "Bearer mgmt-secret",
			wantAvailable: true,
			wantPctApprox: 79.84,
		},
		{
			name:          "models.json fallback with env ref",
			usagePath:     "/api/subscription/v1/usage",
			envKey:        "PI_NANO_KEY",
			envVal:        "from-models-env",
			modelsJSON:    `{"providers":{"nanogpt":{"apiKey":"$PI_NANO_KEY"}}}`,
			statusCode:    http.StatusOK,
			body:          nanoGPTFixture,
			wantHeaderKey: "x-api-key",
			wantHeaderVal: "from-models-env",
			wantAvailable: true,
			wantPctApprox: 79.84,
		},
		{
			name:          "models.json fallback with shell command",
			usagePath:     "/api/subscription/v1/usage",
			modelsJSON:    `{"providers":{"nanogpt":{"apiKey":"!printf cmd-token"}}}`,
			statusCode:    http.StatusOK,
			body:          nanoGPTFixture,
			wantHeaderKey: "x-api-key",
			wantHeaderVal: "cmd-token",
			wantAvailable: true,
			wantPctApprox: 79.84,
		},
		{
			name:           "http error status",
			usagePath:      "/api/subscription/v1/usage",
			envKey:         "NANOGPT_API_KEY",
			envVal:         "test-key",
			statusCode:     http.StatusUnauthorized,
			body:           `{"error":"unauthorized"}`,
			wantAvailable:  false,
			wantErrContain: "HTTP 401",
		},
		{
			name:           "missing token",
			usagePath:      "/api/subscription/v1/usage",
			wantAvailable:  false,
			wantErrContain: "not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NANOGPT_MANAGEMENT_TOKEN", "")
			t.Setenv("NANOGPT_API_KEY", "")
			if tc.envKey != "" {
				t.Setenv(tc.envKey, tc.envVal)
			}

			var gotHeader string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.wantHeaderKey != "" {
					gotHeader = r.Header.Get(tc.wantHeaderKey)
				}
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			modelsPath := filepath.Join(t.TempDir(), "models.json")
			if tc.modelsJSON != "" {
				if err := os.WriteFile(modelsPath, []byte(tc.modelsJSON), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			reader := NanoGPTReader{HTTPClient: srv.Client(), ModelsPath: modelsPath}
			q := reader.Read(t.Context(), "pi", prefs.BurnProfile{
				Reader:   "nanogpt",
				TokenEnv: tc.tokenEnv,
				UsageURL: srv.URL + tc.usagePath,
			}, nil)

			if q.Available != tc.wantAvailable {
				t.Fatalf("Available = %v, want %v (err=%q)", q.Available, tc.wantAvailable, q.Error)
			}
			if tc.wantErrContain != "" {
				if !strings.Contains(q.Error, tc.wantErrContain) {
					t.Fatalf("Error = %q, want substring %q", q.Error, tc.wantErrContain)
				}
				return
			}
			if gotHeader != tc.wantHeaderVal {
				t.Fatalf("header %s = %q, want %q", tc.wantHeaderKey, gotHeader, tc.wantHeaderVal)
			}
			if len(q.Windows) == 0 || math.Abs(q.Windows[0].RemainingPct-tc.wantPctApprox) > 0.05 {
				t.Fatalf("unexpected windows: %+v", q.Windows)
			}
		})
	}
}

// usageBarFixture is `ai-usagebar usage --json` (v1.52.3) captured 2026-11-26,
// with a second account and an extra-usage metric added.
const usageBarFixture = `{"entries":[
{"brand":"anthropic","display_name":"Claude · team-a","error":null,"id":"anthropic@team-a","metrics":[
 {"detail":"Resets in 4h 50m","headline":"percent","label":"Session (5h)","percent":8,"reset_at":"2026-11-26T22:50:00.101936Z","severity":"low","value":"8%","window_secs":18000},
 {"detail":"Resets in 3d 23h","headline":"percent","label":"Weekly (7d)","percent":23,"reset_at":"2026-11-30T17:00:00.101960Z","severity":"low","value":"23%","window_secs":604800}],
 "name":"anthropic · team-a","plan":"Claude Team","status":"ready"},
{"brand":"anthropic","error":null,"id":"anthropic@personal","metrics":[
 {"label":"Weekly (7d)","percent":90,"reset_at":"2026-11-29T10:00:00Z"},
 {"label":"Extra usage","percent":5}],"status":"ready"},
{"brand":"anthropic","error":"token expired; run claude login","id":"anthropic@broken","metrics":[]},
{"brand":"openai","error":null,"id":"openai@team-a","metrics":[{"label":"Weekly (7d)","percent":99,"reset_at":"2026-11-27T00:00:00Z"}]},
{"brand":"anthropic","error":"","id":"anthropic@odd","metrics":[{"label":"Weekly (7d)","percent":50,"reset_at":""}]}
],"primary":null,"schema_version":1}`

func TestParseUsageBarJSON(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		account     string
		wantErr     bool
		wantOverage bool
		want        []Window
	}{
		{
			name:    "account picks its own entry",
			input:   usageBarFixture,
			account: "team-a",
			want: []Window{
				{Group: "anthropic@team-a", Kind: "5h", Label: "Session (5h)", RemainingPct: 92, ResetAt: time.Date(2026, 11, 26, 22, 50, 0, 101936000, time.UTC)},
				{Group: "anthropic@team-a", Kind: "weekly", Label: "Weekly (7d)", RemainingPct: 77, ResetAt: time.Date(2026, 11, 30, 17, 0, 0, 101960000, time.UTC)},
			},
		},
		{
			name:        "extra usage metric flags overage",
			input:       usageBarFixture,
			account:     "personal",
			wantOverage: true,
			want:        []Window{{Group: "anthropic@personal", Kind: "weekly", Label: "Weekly (7d)", RemainingPct: 10, ResetAt: time.Date(2026, 11, 29, 10, 0, 0, 0, time.UTC)}},
		},
		{
			name:    "empty error and bad reset keep the seat, reset unknown",
			input:   usageBarFixture,
			account: "odd",
			want:    []Window{{Group: "anthropic@odd", Kind: "weekly", Label: "Weekly (7d)", RemainingPct: 50}},
		},
		{
			name:  "other vendors are ignored",
			input: `{"entries":[{"id":"openai@x","metrics":[{"label":"Weekly (7d)","percent":1}]},{"id":"anthropic","error":null,"metrics":[{"label":"Weekly (7d)","percent":40,"reset_at":"2026-11-29T10:00:00Z"}]}]}`,
			want:  []Window{{Group: "anthropic", Kind: "weekly", Label: "Weekly (7d)", RemainingPct: 60, ResetAt: time.Date(2026, 11, 29, 10, 0, 0, 0, time.UTC)}},
		},
		{name: "entry error is reported", input: usageBarFixture, account: "broken", wantErr: true},
		{name: "unknown account", input: usageBarFixture, account: "team", wantErr: true},
		{name: "no account with several entries", input: usageBarFixture, wantErr: true},
		{name: "waybar json is not a report", input: `{"text":"8%","tooltip":"x","class":"low"}`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, overage, err := ParseUsageBarJSON([]byte(tc.input), tc.account)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got windows=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if overage != tc.wantOverage {
				t.Fatalf("overageOn = %v, want %v", overage, tc.wantOverage)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d windows, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				assertWindowEqual(t, got[i], tc.want[i])
			}
		})
	}
}

func TestParseTasksJSON(t *testing.T) {
	t.Helper()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    []Task
	}{
		{
			name: "sorts by urgency desc then uuid asc and normalizes effort",
			input: `[
				{"uuid":"bbbbbbbb-1111-1111-1111-111111111111","id":2,"description":"Tie second","project":"fleet","tags":["burn"],"urgency":8.5,"effort":"light","dir":"/tmp/repo-b"},
				{"uuid":"aaaaaaaa-1111-1111-1111-111111111111","id":1,"description":"Tie first","project":"fleet","tags":["burn"],"urgency":8.5,"dir":"/tmp/repo-a"},
				{"uuid":"cccccccc-1111-1111-1111-111111111111","id":3,"description":"Top priority","project":"fleet","tags":["burn"],"urgency":12.3,"effort":"pro","dir":"/tmp/repo-c"},
				{"uuid":"dddddddd-1111-1111-1111-111111111111","id":4,"description":"   ","urgency":99.0},
				{"uuid":"eeeeeeee-1111-1111-1111-111111111111","id":5,"description":"Standard effort","urgency":2.0,"effort":"standard"}
			]`,
			want: []Task{
				{UUID: "cccccccc-1111-1111-1111-111111111111", ID: 3, Description: "Top priority", Effort: "pro", Dir: "/tmp/repo-c", Urgency: 12.3},
				{UUID: "aaaaaaaa-1111-1111-1111-111111111111", ID: 1, Description: "Tie first", Effort: "standard", Dir: "/tmp/repo-a", Urgency: 8.5},
				{UUID: "bbbbbbbb-1111-1111-1111-111111111111", ID: 2, Description: "Tie second", Effort: "light", Dir: "/tmp/repo-b", Urgency: 8.5},
				{UUID: "eeeeeeee-1111-1111-1111-111111111111", ID: 5, Description: "Standard effort", Effort: "standard", Urgency: 2.0},
			},
		},
		{name: "empty array", input: `[]`, want: []Task{}},
		{name: "invalid json", input: `[broken`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTasksJSON([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d tasks, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i].UUID != tc.want[i].UUID || got[i].Description != tc.want[i].Description ||
					got[i].Effort != tc.want[i].Effort || got[i].Dir != tc.want[i].Dir ||
					got[i].Urgency != tc.want[i].Urgency {
					t.Fatalf("task[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestCompanionReadersWithMockRunner(t *testing.T) {
	t.Helper()

	origLookPath, origRunner := companion.LookPath, companion.Runner
	t.Cleanup(func() {
		companion.LookPath = origLookPath
		companion.Runner = origRunner
	})

	companion.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	companion.Runner = func(_ context.Context, path string, args []string, o companion.Opts) ([]byte, error) {
		switch filepath.Base(path) {
		case "agy":
			return []byte(agyFixture), nil
		case "ai-usagebar":
			if strings.Join(args, " ") != "usage --json" {
				t.Fatalf("ai-usagebar args = %v, want usage --json", args)
			}
			return []byte(usageBarFixture), nil
		case "task":
			return []byte(`[{"uuid":"11111111-0000-0000-0000-000000000000","description":"Burn task","urgency":5.0}]`), nil
		default:
			t.Fatalf("unexpected binary %s %v", path, args)
			return nil, nil
		}
	}

	if q := (AgyReader{}).Read(t.Context(), "agy", prefs.BurnProfile{}, nil); !q.Available || len(q.Windows) != 5 {
		t.Fatalf("AgyReader.Read = %+v", q)
	}
	if q := (UsageBarReader{}).Read(t.Context(), "claude-team-a", prefs.BurnProfile{Account: "team-a"}, nil); !q.Available || len(q.Windows) != 2 {
		t.Fatalf("UsageBarReader.Read = %+v", q)
	}
	if tasks, err := ReadTasks(t.Context(), "burn"); err != nil || len(tasks) != 1 || tasks[0].NormalizedEffort() != "standard" {
		t.Fatalf("ReadTasks = %+v, err=%v", tasks, err)
	}
}

func assertWindowEqual(t *testing.T, got, want Window) {
	t.Helper()
	if got.Group != want.Group || got.Kind != want.Kind || got.Label != want.Label ||
		math.Abs(got.RemainingPct-want.RemainingPct) > 0.001 || !got.ResetAt.Equal(want.ResetAt) {
		t.Fatalf("window mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}
