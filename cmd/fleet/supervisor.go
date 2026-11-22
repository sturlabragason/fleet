package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/todo"
	"github.com/sturlabragason/fleet/internal/tools"
)

// confirmResetArgs is the tmux command behind M-R and the overview's R.
func confirmResetArgs(exe string) []string { return confirmResetArgsOf(exe, supervisor.Main) }

// confirmResetArgsOf is confirmResetArgs for admiral adm.
func confirmResetArgsOf(exe, adm string) []string {
	flag, label := "", "admiral"
	if adm != supervisor.Main {
		flag, label = " --name "+tools.Quote(adm), "admiral "+adm
	}
	// Output is discarded: run-shell would show it in view mode over the pane;
	// the result reaches the user via display-message.
	// tmux parses the run-shell argument again: double-quote it for tmux and
	// shell-quote the exe for sh.
	sh := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(tools.Quote(exe) + " admiral reset" + flag + " >/dev/null 2>&1")
	return []string{"confirm-before", "-p", "reset " + label + "? (y/n)", `run-shell -b "` + sh + `"`}
}

// stderr is where deprecation warnings go (replaced in tests).
var stderr io.Writer = os.Stderr

// commandName maps a subcommand alias to the name run() dispatches on:
// "supervisor" is the deprecated pre-ADR-26 name of "admiral".
func commandName(cmd string) string {
	if cmd == "supervisor" {
		return "admiral"
	}
	return cmd
}

// cmdAdmiral runs `fleet admiral ...`; name "supervisor" is the deprecated
// pre-ADR-26 alias (still called by older binaries) and warns on stderr.
func cmdAdmiral(f *fleet.Fleet, name string, args []string) error {
	if name == "supervisor" {
		fmt.Fprintln(stderr, `fleet: "supervisor" is deprecated; use "fleet admiral"`)
	}
	return cmdSupervisor(f, args)
}

// admiralEnv is $FLEET_ADMIRAL_<key>, else the deprecated
// $FLEET_SUPERVISOR_<key>.
func admiralEnv(key string) string {
	if v := os.Getenv("FLEET_ADMIRAL_" + key); v != "" {
		return v
	}
	return os.Getenv("FLEET_SUPERVISOR_" + key)
}

// display shows msg literally on the fleet client (no format/strftime expansion).
func display(f *fleet.Fleet, msg string) { f.T.Run("display-message", "-l", msg) }

// refuseExternal stops a reset or profile swap aimed at an external admiral:
// it has no pane, and acting on one would hit another admiral's (ADR-159).
func refuseExternal(f *fleet.Fleet, adm, verb string) error {
	if supervisor.IsExternal(f.Home, adm) {
		return fmt.Errorf("admiral %s is external (an MCP client outside tmux): fleet cannot %s it; start a new chat in that client instead", adm, verb)
	}
	return nil
}

func mainTarget() string { return fleet.Session + ":main" }

func cmdSupervisor(f *fleet.Fleet, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "add":
			return cmdAdmiralAdd(f, args[1:])
		case "ls":
			return cmdAdmiralLs(f)
		case "close":
			return cmdAdmiralClose(f, args[1:])
		case "give":
			return cmdAdmiralGive(f, args[1:])
		case "min":
			return cmdAdmiralMin(f, args[1:])
		case "act":
			return cmdAdmiralAct(f, args[1:])
		case "reply":
			return cmdAdmiralReply(f, args[1:])
		}
	}
	if len(args) > 0 && args[0] == "profile" {
		return cmdAdmiralProfile(f, args[1:])
	}
	if len(args) > 0 && args[0] == "restart" {
		return cmdAdmiralRestart(f, args[1:])
	}
	if len(args) > 0 && args[0] == "reset" {
		fs := flag.NewFlagSet("admiral reset", flag.ContinueOnError)
		handoff := fs.Bool("handoff", false, "called after fleet_handoff wrote a handoff (no stub)")
		adm, rest, err := nameFlag(f, args[1:])
		if err != nil {
			return err
		}
		if err := fs.Parse(rest); err != nil {
			return err
		}
		gen, err := resetSupervisorOf(f, adm, *handoff)
		if err != nil {
			display(f, err.Error())
			return err
		}
		msg := fmt.Sprintf("%s reset · gen %d", admiralLabel(adm), gen)
		display(f, msg)
		fmt.Println(msg)
		return nil
	}
	adm, args, err := nameFlag(f, args)
	if err != nil {
		return err
	}
	asJSON := len(args) > 0 && args[0] == "--json"
	if len(args) > 0 && !asJSON {
		return fmt.Errorf("usage: fleet admiral [--name N] [--json] | add NAME [--profile P | --external] | ls | close NAME | give AGENT --to NAME | reset [--name N] [--handoff] | profile [--name N] [NAME|--pick] | restart [--name N] | reply [--name N]")
	}
	s := supervisor.CurrentOf(f.Home, f.T, mainTarget(), adm)
	used, max, ok := supervisor.Fill(supervisor.CopilotHome(), s.SessionID)
	if asJSON {
		out := map[string]any{"session_id": s.SessionID, "profile": s.Profile, "pane_id": s.PaneID,
			"generation": s.Generation, "previous": s.Previous, "ctx_pct": nil}
		if !s.Started.IsZero() {
			out["started"] = s.Started
			out["uptime_s"] = int(time.Since(s.Started).Seconds())
		}
		if ok {
			out["ctx_pct"], out["ctx_used"], out["ctx_max"] = supervisor.Pct(used, max), used, max
		}
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		return e.Encode(out)
	}
	fmt.Println(supLine(s, used, max, ok, time.Now()))
	return nil
}

// supLine is `admiral 3f2a91c · claude-personal · ctx 34% (68k/200k) · gen 2 · up 2h13m`.
func supLine(s supervisor.Session, used, max int, ok bool, now time.Time) string {
	id := s.SessionID
	if len(id) > 7 {
		id = id[:7]
	}
	if id == "" {
		id = "?"
	}
	ctx := "ctx ?"
	if ok {
		ctx = fmt.Sprintf("ctx %d%% (%s/%s)", supervisor.Pct(used, max), kTok(used), kTok(max))
	}
	parts := []string{"admiral " + id}
	if s.Profile != "" {
		parts = append(parts, s.Profile)
	}
	parts = append(parts, ctx)
	if s.Generation > 0 {
		parts = append(parts, fmt.Sprintf("gen %d", s.Generation))
	}
	if !s.Started.IsZero() {
		d := now.Sub(s.Started)
		up := fmt.Sprintf("%dm", int(d.Minutes()))
		if h := int(d.Hours()); h > 0 {
			up = fmt.Sprintf("%dh%dm", h, int(d.Minutes())%60)
		}
		parts = append(parts, "up "+up)
	}
	return strings.Join(parts, " · ")
}

func kTok(n int) string { return fmt.Sprintf("%dk", (n+500)/1000) }

// resetSupervisor replaces the admiral with a fresh session (DESIGN.md §6.5
// Reset steps 1–4) and returns the new generation.
func resetSupervisor(f *fleet.Fleet, handoff bool) (int, error) {
	return resetSupervisorOf(f, supervisor.Main, handoff)
}

// resetSupervisorOf is resetSupervisor for admiral adm.
func resetSupervisorOf(f *fleet.Fleet, adm string, handoff bool) (int, error) {
	if err := refuseExternal(f, adm, "reset"); err != nil {
		return 0, err
	}
	log := logx.For("admiral")
	d := holdForDraft(f, adm)
	defer func() { restoreDraft(f, adm, d) }()
	unlock, err := supervisor.LockOf(f.Home, adm)
	if err != nil {
		return 0, err
	}
	defer unlock()
	pane, err := supervisor.FindPaneOf(f.T, mainTarget(), adm)
	if err != nil {
		return 0, err
	}
	old := supervisor.CurrentOf(f.Home, f.T, mainTarget(), adm)
	d = withUnanswered(f, adm, old.SessionID, d)
	_, hTime, hOK := supervisor.ReadHandoffOf(f.Home, adm)
	stale := !hOK || (!old.Started.IsZero() && hTime.Before(old.Started)) || (old.Started.IsZero() && !handoff)
	if stale {
		prev := old.SessionID
		if prev == "" {
			prev = "unknown"
		}
		stub := fmt.Sprintf("Manual reset at %s; previous admiral wrote no handoff. Previous session: %s.",
			time.Now().Format("2006-01-02 15:04"), prev)
		if err := supervisor.WriteHandoffOf(f.Home, adm, stub); err != nil {
			return 0, err
		}
	}
	prof := old.Profile
	if _, ok := f.Profiles[prof]; !ok {
		prof = "" // unknown or unset: FLEET_ADMIRAL_PROFILE / default
	}
	sl, err := supervisorCmdOf(f, adm, prof, bootPrompt(d))
	if err != nil {
		return 0, err
	}
	if err := respawnAdmiral(f, pane, sl); err != nil {
		return 0, err
	}
	setAdmiralPaneOptsOf(f, adm, pane, sl.Profile)
	gen := old.Generation + 1
	if gen < 2 {
		gen = 2 // an admiral existed before this one
	}
	ns := supervisor.Session{SessionID: sl.ID, Profile: sl.Profile, PaneID: pane, Started: time.Now(), Dir: sl.Dir, Generation: gen, Previous: old.SessionID}
	if err := supervisor.SaveOf(f.Home, adm, ns); err != nil {
		return 0, err
	}
	pasteBoot(f, pane, sl)
	d.Carried = d.Sent
	log.Info("reset", "pane", pane, "generation", gen, "session_id", sl.ID, "previous", old.SessionID, "stub_handoff", stale, "handoff", handoff)
	return gen, nil
}

func cmdNote(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("note", flag.ContinueOnError)
	agent := fs.String("agent", "", "agent the note is about")
	adm := fs.String("admiral", supervisor.Main, "admiral the note is from")
	pin := fs.Bool("pin", false, "a standing rule: fleet_brief always shows it (unpin by deleting its line from ~/.fleet/pinned.log)")
	open := fs.String("open", "", "also an open item in the todo store: "+strings.Join(todo.AdmiralKinds, ", "))
	itemCtx := fs.String("ctx", "", "the open item's context (default: the agent's dir, else the configured one)")
	done := fs.String("done", "", "close the open item with this id; the text, if any, is the outcome")
	if err := fs.Parse(args); err != nil {
		return err
	}
	text := strings.Join(fs.Args(), " ")
	if *done != "" {
		it, err := f.DoneItem(*adm, *agent, *done, text)
		if err != nil {
			return fmt.Errorf("close %s: %w", *done, err)
		}
		fmt.Println("closed task [" + it.ID() + "] " + it.Description)
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return errors.New(`usage: fleet note [--agent NAME] [--pin] [--open KIND [--ctx CTX]] "text" | fleet note --done ID ["outcome"]`)
	}
	if err := todo.CheckOpen(*open); err != nil {
		return err
	}
	add := supervisor.AppendNoteBy
	if *pin {
		add = supervisor.AppendPinnedBy
	}
	if err := add(f.Home, *adm, *agent, text); err != nil {
		return err
	}
	if *open == "" {
		return nil
	}
	if k, rest := todo.Kind(text); k != "" {
		text = rest
	}
	it, ok, err := f.AddItem(*adm, *open, text, *agent, *itemCtx)
	switch {
	case err != nil:
		return fmt.Errorf("noted, but not added to the todo store: %w", err)
	case !ok:
		fmt.Println("noted; no todo store in this build, so it is in the notes only")
	default:
		fmt.Printf("noted; open task [%s] (fleet note --done %s closes it)\n", it.ID(), it.ID())
	}
	return nil
}
