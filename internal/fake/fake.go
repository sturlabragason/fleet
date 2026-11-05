// Package fake implements a scripted fake agent for development (ADR-8).
package fake

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"os"
	"strings"
	"time"
)

// State is a fake agent state.
type State string

const (
	Working State = "working"
	Waiting State = "waiting"
	Error   State = "error"
	Idle    State = "idle"
)

// Event drives transitions.
type Event string

const (
	Timer Event = "timer"
	Yes   Event = "yes"
	No    Event = "no"
	Input Event = "input"
)

// Next is the pure transition function. r in [0,1) picks branches after work.
func Next(s State, ev Event, r float64) State {
	switch s {
	case Working:
		if ev != Timer {
			return Working
		}
		switch {
		case r < 0.5:
			return Waiting
		case r < 0.7:
			return Error
		default:
			return Idle
		}
	case Waiting:
		switch ev {
		case Yes, Timer:
			return Working
		case No:
			return Error
		}
		return Waiting
	case Error:
		if ev == Input {
			return Working
		}
		return Idle
	case Idle:
		return Working
	}
	return Idle
}

// Durations for each state (min, spread). Waiting times out after a long while.
var durations = map[State][2]time.Duration{
	Working: {10 * time.Second, 10 * time.Second},
	Waiting: {90 * time.Second, 30 * time.Second},
	Error:   {6 * time.Second, 4 * time.Second},
	Idle:    {8 * time.Second, 7 * time.Second},
}

var spin = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	frameEvery = 400 * time.Millisecond // spinner frame
	stepEvery  = 3                      // frames between two tool results
)

// Directive maps a "!word" line to the state it forces. Sending one (fleet
// send <name> "!wait") clears the pane and composes that state, lead-up and
// all, then holds it there: a screenshot or a demo is composed rather than
// waited for. Any directive turns the random timer off for good; plain input
// still drives the agent.
var Directive = map[string]State{
	"!work":  Working,
	"!wait":  Waiting,
	"!error": Error,
	"!done":  Idle,
}

// Run runs the agent loop until in closes.
func Run(name string, in io.Reader, out io.Writer) {
	// Hash the name, the pid and the clock together: agents spawned in the
	// same millisecond would otherwise get neighbouring seeds, and a whole
	// fleet would pick the same task.
	h := fnv.New64a()
	fmt.Fprintf(h, "%s/%d/%d", name, os.Getpid(), time.Now().UnixNano())
	rng := rand.New(rand.NewSource(int64(h.Sum64())))
	scripts := pool(name)
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			lines <- strings.TrimSpace(sc.Text())
		}
		close(lines)
	}()
	fmt.Fprintf(out, "fakeagent %s ready (tool: fake)\n", name)

	task := scripts[rng.Intn(len(scripts))]
	state, step, frame, started, footer, held := Working, 0, 0, time.Now(), false, false

	// The footer is redrawn in place, the way a real agent's status line is, so
	// the pane keeps a readable transcript instead of a wall of spinner lines.
	clearFooter := func() {
		if footer {
			fmt.Fprint(out, "\r\x1b[2K")
			footer = false
		}
	}
	drawFooter := func() {
		fmt.Fprintf(out, "%s working · %ds · (esc to interrupt)", spin[frame%len(spin)], int(time.Since(started).Seconds()))
		footer = true
	}
	say := func(format string, a ...any) {
		clearFooter()
		fmt.Fprintf(out, format, a...)
	}
	enter := func(s State) <-chan time.Time {
		step, started = 0, time.Now()
		switch s {
		case Working:
			say("\n● Task: %s\n", task.Title)
			drawFooter()
		case Waiting:
			say("\n● I want to %s  (./%s)\nApprove? [y/n] ", task.Ask, name)
		case Error:
			say("\n✖ Error: %s\n", task.Fail[0])
			for _, l := range task.Fail[1:] {
				fmt.Fprintf(out, "  %s\n", l)
			}
		case Idle:
			say("\n✔ Done: %s · %s\n> ", task.Title, task.Done)
		}
		if held {
			return nil
		}
		d := durations[s]
		return time.After(d[0] + time.Duration(rng.Int63n(int64(d[1]))))
	}
	timer := enter(state)
	tick := time.NewTicker(frameEvery)
	defer tick.Stop()
	for {
		ev := Event("")
		var line string
		select {
		case <-tick.C:
			if state != Working {
				continue
			}
			frame++
			switch {
			case frame%stepEvery != 0:
				clearFooter()
			case step < len(task.Steps):
				say("  ✓ %s\n", task.Steps[step])
				step++
			case step-len(task.Steps) < len(task.Say):
				if step == len(task.Steps) {
					say("\n")
				}
				say("%s\n", task.Say[step-len(task.Steps)])
				step++
			default:
				clearFooter()
			}
			drawFooter()
			continue
		case <-timer:
			ev = Timer
		case l, ok := <-lines:
			if !ok {
				return
			}
			line = l
			switch strings.ToLower(l) {
			case "y", "yes":
				ev = Yes
			case "n", "no":
				ev = No
			default:
				ev = Input
			}
		}
		if word, rest, _ := strings.Cut(line, " "); ev == Input && word != "" {
			if next, ok := Directive[strings.ToLower(word)]; ok {
				held, footer = true, false
				// Wipe the pane (and the shell's echo of the directive), then
				// compose the state with the work that would have led to it.
				fmt.Fprint(out, "\x1b[2J\x1b[H")
				if rest = strings.TrimSpace(rest); rest != "" {
					task = pick(scripts, rest, task)
				}
				if next != Working {
					fmt.Fprintf(out, "● Task: %s\n", task.Title)
					for _, s := range task.Steps {
						fmt.Fprintf(out, "  ✓ %s\n", s)
					}
				}
				state = next
				timer = enter(state)
				continue
			}
		}
		next := Next(state, ev, rng.Float64())
		if state == Waiting && ev == Input {
			say("Please answer y or n.\nApprove? [y/n] ")
			continue
		}
		if ev == Input && line != "" && next == Working {
			task = pick(scripts, line, scripts[rng.Intn(len(scripts))])
		}
		if next == Working && state != Working && ev == Timer && state != Waiting {
			task = scripts[rng.Intn(len(scripts))]
		}
		if state == Waiting {
			say("%s\n", map[Event]string{Yes: "approved", No: "rejected", Timer: "no answer, continuing"}[ev])
		}
		if next == state && ev == Input {
			say("(queued: %s)\n", line)
			continue
		}
		state = next
		timer = enter(state)
	}
}
