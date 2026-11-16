package proc

import "time"

// Info is one process as the resource guard sees it (ADR-122).
type Info struct {
	Proc
	PPID  int
	Comm  string
	Ticks uint64        // user + system CPU time, in clock ticks
	RSS   uint64        // resident memory, bytes
	Age   time.Duration // time since it started
	Owner string        // the owner tag from its environment, "" when untagged
	// Daemon: it leads its own session and has no terminal, like a
	// browser, an ssh master or a tmux server some command started.
	Daemon bool
}

// clockTick is USER_HZ, 100 on every Linux build fleet runs on; darwin's
// ps reports CPU time in 1/100 s too.
const clockTick = 100

// TicksPerSecond converts Ticks to CPU seconds.
const TicksPerSecond = clockTick

// Shells are the commands whose children are an agent's work (a build, a
// test run, a server) rather than the agent itself.
var Shells = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "fish": true}

// Own returns root's process and its descendants, leaving out everything
// below a shell other than root itself: the agent's own processes (the
// tool, its helpers, fleet's host and MCP server), not the work it runs.
func Own(ps []Info, root int) []Info {
	kids := map[int][]int{}
	by := make(map[int]Info, len(ps))
	for _, p := range ps {
		kids[p.PPID] = append(kids[p.PPID], p.PID)
		by[p.PID] = p
	}
	r, ok := by[root]
	if !ok {
		return nil
	}
	out := []Info{r}
	for i := 0; i < len(out); i++ {
		if i > 0 && Shells[out[i].Comm] {
			continue
		}
		for _, k := range kids[out[i].PID] {
			out = append(out, by[k])
		}
	}
	return out
}
