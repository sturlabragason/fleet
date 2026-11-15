package fleet

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/park"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/proc"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/sysx"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// The process guard (DESIGN §7, ADR-122, #48). Every procEvery the
// housekeeping poll reads /proc and:
//   - kills processes tagged with an agent's owner tag (FLEET_OWNER) whose
//     agent has no live pane: what a close, park, crash or admiral restart
//     left behind after the processes left the pane's tree;
//   - parks an agent whose own processes hold more than [memory] agent_max
//     (RSS + swap), whatever its status;
//   - parks an idle agent whose own processes burn more than
//     [memory] agent_max_cpu cores for prefs.AgentCPUFor.
// Every action is reported to the agent's admiral.

// OwnerTag is the FLEET_OWNER value of agent name's processes.
func OwnerTag(home, name string) string { return name + "@" + home }

// procEvery is the least time between two process-guard passes, across
// every process that does housekeeping.
var procEvery = 30 * time.Second

// orphanGrace is how old a tagged process must be before it can count as
// an orphan, so a pane that is still being opened or tagged is never read
// as gone.
var orphanGrace = 2 * time.Minute

// agentCPUFor is how long an idle agent must stay over agent_max_cpu.
var agentCPUFor = prefs.AgentCPUFor

// scanProcs reads the process table; tests replace it.
var scanProcs = func() []proc.Info { return proc.Scan(sysx.OwnerEnv) }

type procGuard struct {
	run sync.Mutex // one pass at a time
	mu  sync.Mutex
	cpu map[string]cpuSample
}

type cpuSample struct {
	at    time.Time
	ticks uint64
}

// Orphan is a leftover process of an agent that has no live pane.
type Orphan struct {
	Agent string
	PID   int
	Comm  string
	Mem   uint64
	// Spared: a daemon some command of the agent started (a browser, an
	// ssh master) that the reaper leaves alone unless it is over agent_max.
	Spared bool
	proc   proc.Info
}

// paneRoots maps each agent with a live pane to that pane's pid. ok is
// false when tmux could not be read (a timeout under load, a permission
// error): then nothing may be called an orphan, as every agent would be.
// Only a plain "no server" means no agent has a pane.
func (f *Fleet) paneRoots() (map[string]int, bool) {
	out, err := f.T.Run("list-panes", "-a", "-F", "#{@fleet_agent}\t#{pane_pid}\t#{pane_dead}")
	roots := map[string]int{}
	if err != nil {
		return roots, tmux.NoServer(err)
	}
	for l := range strings.SplitSeq(out, "\n") {
		fs := strings.Split(l, "\t")
		if len(fs) != 3 || fs[0] == "" || fs[2] == "1" {
			continue
		}
		if pid, err := strconv.Atoi(fs[1]); err == nil && pid > 1 {
			roots[fs[0]] = pid
		}
	}
	return roots, true
}

// orphans picks the tagged processes of this fleet home whose agent has no
// live pane.
func (f *Fleet) orphans(ps []proc.Info, roots map[string]int) []Orphan {
	self := os.Getpid()
	var out []Orphan
	for _, p := range ps {
		name, home, ok := strings.Cut(p.Owner, "@")
		if !ok || home != f.Home || p.PID == self || p.Age < orphanGrace {
			continue
		}
		if _, live := roots[name]; live {
			continue
		}
		out = append(out, Orphan{Agent: name, PID: p.PID, Comm: p.Comm, Mem: p.RSS, proc: p,
			Spared: p.Daemon && p.Comm != "fleet"})
	}
	return out
}

// Orphans lists this fleet's leftover agent processes (for fleet doctor).
func (f *Fleet) Orphans() ([]Orphan, error) {
	roots, ok := f.paneRoots()
	if !ok {
		return nil, fmt.Errorf("reading tmux panes failed")
	}
	orph := f.orphans(scanProcs(), roots)
	for i := range orph {
		orph[i].Mem += proc.Swap(orph[i].PID)
	}
	return orph, nil
}

// guardProcs is one process-guard pass; a pass still running (a kill
// waiting out its grace) makes this one a no-op.
func (f *Fleet) guardProcs(as []*board.Agent, now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			logx.For("fleet").Error("process guard: panic", "err", r)
		}
	}()
	m := f.MemPrefs()
	if !f.procs.run.TryLock() {
		return
	}
	defer f.procs.run.Unlock()
	if ok, _ := f.claimStamp("procguard", now, procEvery); !ok {
		return
	}
	roots, ok := f.paneRoots()
	if !ok {
		return
	}
	ps := scanProcs()
	if len(ps) == 0 {
		return
	}
	// Leftovers of gone agents are reaped even with the guard off: nothing
	// on the board owns them. Parking a live agent is what guard = false stops.
	f.reapOrphans(f.orphans(ps, roots), ps, agentMax(m))
	if m.Guard {
		f.guardAgents(as, ps, roots, now, m)
	}
}

// reapOrphans kills orphans and their descendants with the same tag, and
// tells the admiral of each agent once per pass. A spared daemon is killed
// only over capMem (0: never).
func (f *Fleet) reapOrphans(orph []Orphan, ps []proc.Info, capMem uint64) {
	kids := map[int][]proc.Info{}
	for _, p := range ps {
		kids[p.PPID] = append(kids[p.PPID], p)
	}
	by := map[string][]Orphan{}
	var kill []proc.Proc
	for _, o := range orph {
		o.Mem += proc.Swap(o.PID)
		if o.Spared && (capMem == 0 || o.Mem <= capMem) {
			continue
		}
		by[o.Agent] = append(by[o.Agent], o)
		// Processes as scanned, so a reused pid is never signalled; an
		// untagged or other-tagged descendant (a user's shell in a tmux
		// server the agent started) is not the agent's.
		for q := []proc.Info{o.proc}; len(q) > 0; q = q[1:] {
			kill = append(kill, q[0].Proc)
			for _, k := range kids[q[0].PID] {
				if k.Owner == o.proc.Owner {
					q = append(q, k)
				}
			}
		}
	}
	if len(kill) == 0 {
		return
	}
	proc.Kill(kill, treeGrace)
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		var mem uint64
		var pids []string
		for _, o := range by[n] {
			mem += o.Mem
			pids = append(pids, fmt.Sprintf("%d %s", o.PID, o.Comm))
		}
		logx.For("fleet").Warn("process guard: killed leftover processes", "agent", n, "procs", pids, "mem", park.Bytes(mem))
		f.tellAdmiralBox(f.AdmiralBoxOf(n), fmt.Sprintf("killed %d leftover process(es) of %s that outlived its pane (%s, %s).",
			len(pids), n, strings.Join(pids, ", "), park.Bytes(mem)))
	}
}

// agentMax is the per-agent memory cap in bytes; 0 = no cap.
func agentMax(m prefs.Memory) uint64 {
	switch m.AgentMax {
	case prefs.AgentMaxOff:
		return 0
	case 0:
		total, err := park.Total()
		if err != nil {
			return 0
		}
		return max(total/4, 2<<30)
	}
	return m.AgentMax
}

// guardAgents parks at most one agent per pass: the biggest one over the
// memory cap, else one idle agent over the CPU cap.
func (f *Fleet) guardAgents(as []*board.Agent, ps []proc.Info, roots map[string]int, now time.Time, m prefs.Memory) {
	capMem := agentMax(m)
	var big *board.Agent
	var bigMem uint64
	var hot *board.Agent
	var hotCores float64
	f.procs.mu.Lock()
	seen := map[string]cpuSample{}
	for _, a := range as {
		root, ok := roots[a.Name]
		if !ok || a.Status == string(status.Parked) {
			continue
		}
		own := proc.Own(ps, root)
		var mem, ticks uint64
		for _, p := range own {
			ticks += p.Ticks
		}
		if capMem > 0 {
			// A working agent is judged by its biggest process, not the
			// sum: a tool command exec'd in place (bash -c 'cargo build')
			// does not show as a shell, so its tree would count as the
			// agent's. A leak is one process; a build is many.
			working := a.Status == string(status.Working)
			for _, p := range own {
				pm := p.RSS + proc.Swap(p.PID)
				if working {
					mem = max(mem, pm)
				} else {
					mem += pm
				}
			}
			if mem > capMem && mem > bigMem {
				big, bigMem = a, mem
			}
		}
		if m.AgentMaxCPU <= 0 {
			continue
		}
		s, had := f.procs.cpu[a.Name]
		switch {
		case !had || a.Status != string(status.Idle) || a.StatusSince.After(s.at) || ticks < s.ticks:
			seen[a.Name] = cpuSample{now, ticks}
		case now.Sub(s.at) < agentCPUFor:
			seen[a.Name] = s
		default:
			cores := float64(ticks-s.ticks) / proc.TicksPerSecond / now.Sub(s.at).Seconds()
			if cores >= m.AgentMaxCPU && cores > hotCores {
				hot, hotCores = a, cores
			}
			seen[a.Name] = cpuSample{now, ticks}
		}
	}
	f.procs.cpu = seen
	f.procs.mu.Unlock()
	switch {
	case big != nil:
		f.parkRunaway(big, fmt.Sprintf("its processes hold %s, over agent_max %s", park.Bytes(bigMem), park.Bytes(capMem)))
	case hot != nil:
		f.parkRunaway(hot, fmt.Sprintf("idle but burning %.1f cores for %s, over agent_max_cpu %.1f", hotCores, agentCPUFor, m.AgentMaxCPU))
	}
}

func (f *Fleet) parkRunaway(a *board.Agent, reason string) {
	log := logx.For("fleet")
	if err := f.ParkRunaway(a.Name, "process guard: "+reason); err != nil {
		log.Warn("process guard: park failed", "agent", a.Name, "reason", reason, "err", err)
		f.tellAdmiralBox(f.AdmiralBoxOf(a.Name), fmt.Sprintf("%s is a runaway (%s) but parking it failed: %v. Close it or stop its processes.", a.Name, reason, err))
		return
	}
	log.Warn("process guard: parked", "agent", a.Name, "status", a.Status, "reason", reason)
	wake := fmt.Sprintf("Wake it with fleet_park name=%s action=wake if it is needed.", a.Name)
	if a.SessionID == "" {
		wake = "It had no session id, so it can't be woken; spawn it again if it is needed."
	}
	f.tellAdmiralBox(f.AdmiralBoxOf(a.Name), fmt.Sprintf("parked %s (was %s): %s. %s", a.Name, a.Status, reason, wake))
}
