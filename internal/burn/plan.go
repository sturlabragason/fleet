package burn

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/runs"
)

// ActiveAgent is the minimal board snapshot the planner needs.
type ActiveAgent struct {
	Name    string
	Profile string
	Working bool
	// Live: still on the board and not reported done (waiting, idle, stuck,
	// queued...). A live burn agent counts toward max_concurrent and gets the
	// wrap-up message.
	Live bool
}

// PlanInput bundles everything Plan needs so it stays pure and testable.
type PlanInput struct {
	Now        time.Time
	Config     prefs.Burn
	MaxWorking int // from [defaults] max_working; 0 = unlimited
	Quotas     []Quota
	Runs       []runs.Record
	Tasks      []Task
	TaskError  string
	Active     []ActiveAgent
	DefaultDir string // fallback repo dir when a task has no dir UDA
}

// InferReader picks a reader name when [burn.<profile>] reader is unset.
func InferReader(profile, command string) string {
	p := strings.ToLower(profile)
	c := strings.ToLower(filepath.Base(command))
	switch {
	case p == "agy" || c == "agy":
		return "agy"
	case p == "pi" || c == "pi":
		return "nanogpt"
	case strings.HasPrefix(p, "claude") || c == "claude":
		return "ai-usagebar"
	default:
		return ""
	}
}

// NewReader returns the concrete Reader for name ("agy", "nanogpt", "ai-usagebar").
func NewReader(name string) Reader {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "agy":
		return &AgyReader{}
	case "nanogpt", "pi":
		return &NanoGPTReader{}
	case "ai-usagebar", "usagebar", "claude":
		return &UsageBarReader{}
	default:
		return nil
	}
}

// Plan computes wrap-ups and spawns for one tick without side effects.
func Plan(in PlanInput) TickResult {
	res := TickResult{
		At:         in.Now,
		DryRun:     in.Config.DryRun,
		TasksFound: len(in.Tasks),
		TaskError:  in.TaskError,
		Profiles:   make([]ProfilePlan, 0, len(in.Quotas)),
	}

	activeNames := make(map[string]bool, len(in.Active))
	activeBurn := 0
	totalWorking := 0
	for _, a := range in.Active {
		activeNames[a.Name] = true
		if a.Working {
			totalWorking++
		}
		if a.Live && strings.HasPrefix(a.Name, "burn-") {
			activeBurn++
		}
	}

	// Build candidate task queue excluding tasks already running as burn-<id8>.
	var queue []Task
	for _, t := range in.Tasks {
		if !activeNames[t.AgentName()] {
			queue = append(queue, t)
		}
	}
	claimed := make(map[string]bool, len(queue))

	for _, q := range in.Quotas {
		pp := planProfile(in, q, &activeBurn, &totalWorking, queue, claimed)
		res.Profiles = append(res.Profiles, pp)
	}
	return res
}

func planProfile(in PlanInput, q Quota, activeBurn, totalWorking *int, queue []Task, claimed map[string]bool) ProfilePlan {
	pcfg := in.Config.ForProfile(q.Profile)
	pp := ProfilePlan{
		Profile: q.Profile,
		Reader:  q.Reader,
		Quota:   q,
	}
	if !q.Available {
		pp.SkipReason = "quota unavailable: " + q.Error
		return pp
	}
	if q.OverageOn && !in.Config.OverageAllowed(q.Profile) {
		pp.SkipReason = "overage/extra usage is enabled"
		return pp
	}
	w, ok := q.PrimaryWindow(pcfg.QuotaWindow)
	if !ok {
		pp.SkipReason = "no quota window reported"
		return pp
	}
	pp.Window = &w
	if w.ResetAt.IsZero() {
		pp.SkipReason = "reset time unknown"
		return pp
	}
	ttr := w.ResetAt.Sub(in.Now)
	pp.TimeToReset = ttr
	if ttr <= 0 {
		pp.SkipReason = "window already reset"
		return pp
	}

	// Wrap-up check: if reset is within WrapUpBefore, ask running burn agents on
	// this profile to commit and finish, and do not start new ones.
	if in.Config.WrapUpBefore > 0 && ttr <= in.Config.WrapUpBefore {
		for _, a := range in.Active {
			if a.Live && strings.HasPrefix(a.Name, "burn-") && a.Profile == q.Profile {
				pp.WrapUps = append(pp.WrapUps, WrapUpPlan{
					Agent:       a.Name,
					Profile:     q.Profile,
					TimeToReset: ttr,
				})
			}
		}
		pp.SkipReason = fmt.Sprintf("within wrap-up window (%s before reset)", ttr.Round(time.Minute))
		return pp
	}

	if ttr > pcfg.Window {
		pp.SkipReason = fmt.Sprintf("outside burn window (%s until reset > %s)", ttr.Round(time.Minute), pcfg.Window)
		return pp
	}
	pp.InWindow = true

	if w.RemainingPct <= pcfg.ReservePct {
		pp.SkipReason = fmt.Sprintf("at or below reserve (%.1f%% <= %.1f%%)", w.RemainingPct, pcfg.ReservePct)
		return pp
	}

	// Compute N = floor((left - reserve) / mean_cost).
	n, meanCost, unit, samples := estimateN(in.Runs, q.Profile, w, pcfg, in.Config.MinSamples)
	pp.MeanCost = meanCost
	pp.CostUnit = unit
	pp.Samples = samples
	pp.ComputedN = n
	if n <= 0 {
		pp.SkipReason = fmt.Sprintf("remaining above reserve fits 0 runs (mean %.1f %s)", meanCost, unit)
		return pp
	}

	// Apply caps: max_per_tick, max_concurrent burn agents, fleet max_working.
	capped := min(n, pcfg.MaxPerTick)
	if in.Config.MaxConcurrent > 0 {
		rem := max(in.Config.MaxConcurrent-*activeBurn, 0)
		capped = min(capped, rem)
	}
	if in.MaxWorking > 0 {
		rem := max(in.MaxWorking-*totalWorking, 0)
		capped = min(capped, rem)
	}
	pp.CappedN = capped
	if capped <= 0 {
		pp.SkipReason = "concurrency cap reached"
		return pp
	}

	for _, t := range queue {
		if len(pp.PlannedSpawns) >= capped {
			break
		}
		if claimed[t.UUID] || !projectAllowed(t.Project, pcfg.Projects) {
			continue
		}
		claimed[t.UUID] = true
		dir := t.WorkDir(in.DefaultDir)
		effort := t.NormalizedEffort()
		if effort == "pro" {
			// A pro agent may merge into main itself (donegate.mayMerge); burn work stays on its branch for review.
			effort = "standard"
		}
		pp.PlannedSpawns = append(pp.PlannedSpawns, SpawnPlan{
			Agent:   t.AgentName(),
			Profile: q.Profile,
			Task:    t,
			Dir:     dir,
			Effort:  effort,
			Prompt:  BuildPrompt(t, q.Profile, w),
		})
		*activeBurn++
		*totalWorking++
	}
	if len(pp.PlannedSpawns) == 0 {
		pp.SkipReason = fmt.Sprintf("no pending +%s tasks available", in.Config.Tag)
	}
	return pp
}

func projectAllowed(project string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(project)) {
			return true
		}
	}
	return false
}

// estimateN computes floor((left - reserve) / meanCost) from runs.jsonl spend
// or the configured fallback cost.
func estimateN(history []runs.Record, profile string, w Window, pcfg prefs.BurnProfile, minSamples int) (n int, mean float64, unit string, samples int) {
	if minSamples < 1 {
		minSamples = prefs.DefaultBurnMinSamples
	}
	var sumTokens int64
	for _, r := range history {
		if r.Profile == profile && r.Spend != nil && r.Spend.Tokens > 0 {
			sumTokens += r.Spend.Tokens
			samples++
		}
	}

	// Token-denominated quota (e.g. NanoGPT 60M weekly input tokens).
	if w.LimitTokens > 0 && w.RemainingTokens > 0 {
		meanTok := float64(pcfg.DefaultCostTokens)
		if samples >= minSamples {
			meanTok = float64(sumTokens) / float64(samples)
		}
		if meanTok <= 0 {
			return 0, 0, "tokens", samples
		}
		reserveTok := float64(w.LimitTokens) * pcfg.ReservePct / 100.0
		avail := float64(w.RemainingTokens) - reserveTok
		if avail <= 0 {
			return 0, meanTok, "tokens", samples
		}
		return int(math.Floor(avail / meanTok)), meanTok, "tokens", samples
	}

	// Percentage-denominated quota (e.g. agy, ai-usagebar).
	meanPct := pcfg.DefaultCostPct
	if samples >= minSamples && pcfg.LimitTokens > 0 {
		meanTok := float64(sumTokens) / float64(samples)
		meanPct = (meanTok / float64(pcfg.LimitTokens)) * 100.0
	}
	if meanPct <= 0 {
		return 0, 0, "pct", samples
	}
	avail := w.RemainingPct - pcfg.ReservePct
	if avail <= 0 {
		return 0, meanPct, "pct", samples
	}
	return int(math.Floor(avail / meanPct)), meanPct, "pct", samples
}

// BuildPrompt produces the worktree-only burn task prompt.
func BuildPrompt(t Task, profile string, w Window) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Quota-burn task [%s] (profile %s, reset %s).\n\n", t.ShortID(), profile, w.ResetAt.UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "Task: %s\n", t.Description)
	if t.Project != "" {
		fmt.Fprintf(&b, "Project: %s\n", t.Project)
	}
	b.WriteString("\nBurn rules (strict):\n")
	b.WriteString("- Work ONLY in your assigned worktree and branch (fleet/" + t.AgentName() + ").\n")
	b.WriteString("- Commit your changes cleanly and write result.md summarizing what you changed and how you verified it.\n")
	b.WriteString("- Do NOT merge to main, do NOT push main, do NOT cut a release, and do NOT write to any external tracker, mail, or chat.\n")
	b.WriteString("- Your branch will be reviewed from the morning Accept/Reject page.\n")
	return b.String()
}

// WrapUpMessage is sent to a running burn agent before its profile's quota resets.
func WrapUpMessage(profile string, ttr time.Duration) string {
	return fmt.Sprintf("Quota for %s resets in %s. Please wrap up now: commit your current work on your branch, update result.md with what is done and what remains, and report done. Do not merge to main.", profile, ttr.Round(time.Minute))
}

// AppendLog writes res as one JSON line to <home>/logs/burn.jsonl.
func AppendLog(home string, res TickResult) error {
	dir := filepath.Join(home, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create burn log dir: %w", err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("encode burn tick: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "burn.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open burn log: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("write burn log: %w", err)
	}
	return f.Close()
}

// ActiveFromBoard converts board agents into ActiveAgent entries.
func ActiveFromBoard(agents []*board.Agent) []ActiveAgent {
	out := make([]ActiveAgent, 0, len(agents))
	for _, a := range agents {
		out = append(out, ActiveAgent{
			Name:    a.Name,
			Profile: a.Profile,
			Working: a.Status == "working" || a.Status == "starting",
			Live:    a.Status != "parked" && a.Status != "gone" && (a.Report == nil || a.Report.State != "done"),
		})
	}
	return out
}

func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}
