package fake

// Scripted work for the fake agent (ADR-8). A fake pane is what a reader sees
// in the README screenshot and what `make dev` shows all day, so it prints the
// shape of real coding-agent output (a task line, a few tool results, a live
// footer) instead of a repeated spinner line. The lines are generic on purpose:
// no repository, host or person a screenshot could leak (docs/style.md §5).
//
// The status patterns in internal/status read "● Task:" and "(esc to interrupt)"
// as working, "Approve? [y/n]" as waiting, "✖ Error" as error and "✔ Done" or a
// bare prompt as idle. Keep those four markers, with the parentheses, when
// editing this file; fake_test.go asserts each state still classifies.

// Task is one piece of scripted work: what the agent says it is doing, the tool
// results it prints while doing it, what it asks permission for, how it fails
// and how it finishes.
type Task struct {
	Title string   // shown after "● Task:"
	Steps []string // tool results, one per step tick
	Ask   string   // what it asks approval for, without the question mark
	Fail  []string // the "✖ Error" headline, then its detail lines
	Done  string   // the summary after "✔ Done: <title> · "
	Say   []string // what the agent says once its steps are through
}

// tasks is the pool an agent draws from. Every step line stays at or under
// 50 columns (54 with the "  ✓ " prefix) so it survives a quarter-width wall
// pane without wrapping; TestStepLinesFitAPane holds the bound.
var tasks = []Task{{
	Title: "refactor auth middleware",
	Steps: []string{
		"read internal/auth/middleware.go · 142 lines",
		"grep \"func New\" internal/auth · 6 matches",
		"edit internal/auth/middleware.go · +23 −8",
		"edit internal/auth/token.go · +11 −4",
		"go build ./... · ok 1.2s",
		"go test ./internal/auth/ · ok · 14 tests",
	},
	Ask:  "run `go test ./...`",
	Fail: []string{"go test ./internal/auth/ failed (exit 1)", "token_test.go:88: want 2 claims, got 0"},
	Done: "2 files · +34 −12",
}, {
	Title: "add tests for board store",
	Steps: []string{
		"read internal/board/store.go · 318 lines",
		"new internal/board/store_test.go",
		"edit internal/board/store_test.go · +86 −0",
		"go test ./internal/board/ · ok · 23 tests",
		"go vet ./internal/board/ · clean",
	},
	Ask:  "run `go test ./internal/board/`",
	Fail: []string{"go test ./internal/board/ failed (exit 1)", "store_test.go:142: want 3 rows, got 0"},
	Done: "1 file · +86 −0 · 9 tests",
}, {
	Title: "fix flaky e2e",
	Steps: []string{
		"go test ./e2e/ -run Checkout x20 · 3 failed",
		"read e2e/checkout_test.go · 204 lines",
		"edit e2e/checkout_test.go · +9 −3",
		"go test ./e2e/ -run Checkout x50 · ok 21s",
	},
	Ask:  "run `go test ./e2e/ -count 50`",
	Fail: []string{"go test ./e2e/ failed (exit 1)", "checkout_test.go:61: timeout after 30s"},
	Done: "1 file · +9 −3 · 50/50 green",
}, {
	Title: "update deploy config",
	Steps: []string{
		"read deploy/values.yaml · 96 lines",
		"edit deploy/values.yaml · +6 −2",
		"helm template deploy/ · 14 manifests",
		"kubeconform -strict · 14 passed",
	},
	Ask:  "apply the rendered manifests to staging",
	Fail: []string{"kubeconform -strict failed (exit 1)", "values.yaml:41: memory is not a quantity"},
	Done: "1 file · +6 −2 · 14 manifests",
}, {
	Title: "add search endpoint",
	Steps: []string{
		"read api/routes.go · 211 lines",
		"new api/search.go",
		"edit api/search.go · +74 −0",
		"edit api/routes.go · +4 −0",
		"go test ./api/ · ok · 31 tests · 1.1s",
	},
	Ask:  "run `go test ./api/`",
	Fail: []string{"go build ./api/ failed (exit 1)", "search.go:52: undefined: index.Query"},
	Done: "2 files · GET /v1/search",
}, {
	Title: "bump dependencies",
	Steps: []string{
		"go list -m -u all · 7 updates",
		"go get -u ./... · 7 modules",
		"go mod tidy · go.sum +12 −9",
		"go build ./... · ok 2.4s",
		"go test ./... · ok · 148 tests · 9.6s",
	},
	Ask:  "run `go test ./...` after the bump",
	Fail: []string{"go mod tidy failed (exit 1)", "ambiguous import: 2 versions of x/net"},
	Done: "7 modules · 148 tests green",
}, {
	Title: "write migration",
	Steps: []string{
		"read db/schema.sql · 412 lines",
		"new db/migrations/0042_add_sessions.sql",
		"edit 0042_add_sessions.sql · +28 −0",
		"migrate up --dry-run · 0 destructive",
	},
	Ask:  "run `migrate up` against the dev database",
	Fail: []string{"migrate up --dry-run failed (exit 1)", "0042: no relation \"accounts\""},
	Done: "sessions table · reversible",
}, {
	Title: "tidy README",
	Steps: []string{
		"read README.md · 184 lines",
		"edit README.md · +12 −31",
		"markdownlint README.md · clean",
		"check links · 23 ok",
	},
	Ask:  "stage README.md",
	Fail: []string{"check links failed (1 of 23)", "README.md:77: dead link to docs/old-setup.md"},
	Done: "1 file · +12 −31 · 23 links ok",
}}

// admiralTasks is what the admiral pane shows: the fleet_* MCP tools it calls
// while it watches the agents, rather than code edits of its own.
var admiralTasks = []Task{{
	Title: "watch the fleet",
	Steps: []string{
		"fleet_status · 8 agents · 2 waiting · 1 error",
		"fleet_reports · 1 unread · web-nav done",
		"fleet_peek api-auth · wants to run the tests",
		"fleet_send api-auth \"y\" · approved",
		"fleet_peek infra-dns · go mod tidy failed",
		"fleet_note \"infra-dns: pin x/net before retry\"",
		"fleet_spend · 8 agents · 11.40 credits today",
		"fleet_msg_send search-idx \"db-migrate has 0042\"",
	},
	Say: []string{
		"api-auth asked to run the tests; I approved it.",
		"infra-dns failed on `go mod tidy`: two versions of",
		"x/net. I left it holding with a note to pin one.",
		"db-migrate already owns 0042, so I told search-idx",
		"before it writes a second migration.",
		"",
		"Nothing needs you right now.",
	},
	Ask:  "close web-nav, it reported done 4m ago",
	Fail: []string{"fleet_send search-idx failed", "agent search-idx is not running"},
	Done: "1 approval · 1 report read · 1 note",
}, {
	Title: "triage the waiting agents",
	Steps: []string{
		"fleet_status · 2 waiting · longest 3m",
		"fleet_peek search-idx · wants to write a migration",
		"fleet_send search-idx \"y\" · approved",
		"fleet_spend · 8 agents · 11.40 credits today",
	},
	Say: []string{
		"Both approvals were safe: tests in their own",
		"worktrees, no write outside them.",
		"",
		"Longest wait was 3m, on search-idx.",
	},
	Ask:  "spawn a reviewer on the api-auth worktree",
	Fail: []string{"fleet_spawn reviewer failed", "max_working is 8, the queue is full"},
	Done: "2 approvals · 0 left waiting",
}}

// pool returns the scripts an agent of this name draws from.
func pool(name string) []Task {
	if name == "admiral" || name == "supervisor" {
		return admiralTasks
	}
	return tasks
}

// pick returns the script called title, or cur relabelled with it when no
// script matches, so "!work add search endpoint" shows that task's real steps.
func pick(scripts []Task, title string, cur Task) Task {
	for _, t := range scripts {
		if t.Title == title {
			return t
		}
	}
	cur.Title = title
	return cur
}
