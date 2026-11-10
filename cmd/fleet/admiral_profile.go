package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mcp"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tools"
)

// Choosing and swapping the admiral's tool profile (DESIGN §6.10, ADR-27).

// bootPasteTimeout overrides how long a swap waits before pasting the boot
// prompt into a profile without prompt_args; 0 = the profile's ready_timeout.
var bootPasteTimeout time.Duration

// handoffCap is supervisor's handoff cap: the swap handoff stays inside it.
const handoffCap = 4 << 10

// swapTailLines is how much of the outgoing admiral's pane a swap keeps.
const swapTailLines = 40

// resolveAdmiralProfile picks the admiral profile: explicit flag > env
// FLEET_ADMIRAL_PROFILE > FLEET_SUPERVISOR_PROFILE > last choice > config >
// copilot. Names from env, last choice or config that are no longer profiles
// are skipped with a warning; an unknown explicit name is an error.
func resolveAdmiralProfile(explicit string, profiles map[string]tools.Profile, env func(string) string, persisted, config string) (string, []string, error) {
	if explicit != "" {
		if _, ok := profiles[explicit]; !ok {
			return "", nil, fmt.Errorf("no profile %q (have %s)", explicit, strings.Join(tools.Names(profiles), ", "))
		}
		return explicit, nil, nil
	}
	var warns []string
	for _, c := range []struct{ from, name string }{
		{"FLEET_ADMIRAL_PROFILE", env("FLEET_ADMIRAL_PROFILE")},
		{"FLEET_SUPERVISOR_PROFILE", env("FLEET_SUPERVISOR_PROFILE")},
		{"last choice", persisted},
		{"[admiral] profile", config},
	} {
		n := strings.TrimSpace(c.name)
		if n == "" {
			continue
		}
		if _, ok := profiles[n]; ok {
			return n, warns, nil
		}
		warns = append(warns, fmt.Sprintf("admiral profile %q (%s) is not a profile; skipped", n, c.from))
	}
	return "copilot", warns, nil
}

// resolveAdmiral is resolveAdmiralProfile with no flag, from this fleet's
// env, last choice and config. Warnings go to the log and the status line.
func resolveAdmiral(f *fleet.Fleet) string { return resolveAdmiralOf(f, supervisor.Main) }

// resolveAdmiralOf is resolveAdmiral for one admiral: its own last choice or
// registry profile; main also reads the env and config.
func resolveAdmiralOf(f *fleet.Fleet, adm string) string {
	if adm != supervisor.Main {
		if a, ok := supervisor.Find(f.Home, adm); ok && a.Profile != "" {
			if _, ok := f.Profiles[a.Profile]; ok {
				return a.Profile
			}
		}
	}
	prof, warns, _ := resolveAdmiralProfile("", f.Profiles, os.Getenv, supervisor.LoadChoiceOf(f.Home, adm), f.Prefs.AdmiralProfile())
	warnAdmiral(f, warns)
	return prof
}

func warnAdmiral(f *fleet.Fleet, warns []string) {
	for _, w := range warns {
		logx.For("admiral").Warn(w)
		if f.T.HasSession(fleet.Session) {
			display(f, w)
		}
	}
}

// admiralMCPArgs wires the fleet MCP server into the admiral per CLI:
// copilot-pattern profiles get the lean supervisor flags, claude-pattern
// ones an --mcp-config with fleet (stdio) and the fleet tools pre-approved.
// --allowedTools is written with = so the variadic option can't swallow a
// positional boot prompt after it.
func admiralMCPArgs(p tools.Profile, exe string) []string {
	return admiralMCPArgsOf(p, exe, "", supervisor.Main)
}

// admiralMCPArgsOf wires the fleet MCP server of admiral adm, whose args carry
// --admiral adm (main included), so it never has to guess who it serves
// (DESIGN §6.12). Claude gets the config inline. Copilot reads the workspace
// .mcp.json of the dir it starts in: main keeps the shared supervisor/.mcp.json
// (an untagged admiral pane is main), and any other admiral gets its own copy
// in its directory under home, written by admiralLaunchOf, and is started there, so its
// config holds the only "fleet" server rather than racing a same-named one.
// FLEET_ADMIRAL_NAME and the pane tag back it up.
func admiralMCPArgsOf(p tools.Profile, exe, home, adm string) []string {
	mcpArgs := []string{"mcp", "--admiral", adm}
	switch p.Patterns {
	case "copilot":
		return supervisorMCPArgs(p)
	case "claude":
		// --strict-mcp-config: fleet is its only MCP server, as for copilot.
		// The rules go in as a system prompt file: Claude Code cuts MCP
		// server instructions at about 4 KB (ADR-86).
		if home != "" {
			mcpArgs = append(mcpArgs, "--rules-in-prompt")
		}
		cfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fleet": map[string]any{
			"type": "stdio", "command": exe, "args": mcpArgs}}})
		out := []string{"--mcp-config", string(cfg), "--strict-mcp-config", "--allowedTools=mcp__fleet"}
		if home != "" {
			out = append(out, "--append-system-prompt-file", admiralRulesPath(home, adm))
		}
		return out
	}
	return nil
}

// admiralRulesPath is the admiral rules file a claude admiral reads as its
// system prompt (ADR-86).
func admiralRulesPath(home, adm string) string {
	return filepath.Join(supervisor.DirOf(home, adm), "rules.md")
}

// writeAdmiralRules writes the admiral rules for a claude admiral, fresh
// from this binary on every launch.
func writeAdmiralRules(home, adm string) error {
	path := admiralRulesPath(home, adm)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(mcp.Instructions), 0o600)
}

// claudeAdmiralEnv loads every fleet tool up front (no ToolSearch deferral,
// which hid the fleet tools' schemas) and keeps the user's claude.ai
// connectors out of the admiral (ADR-86). It asks for the one-hour prompt
// cache: admiral wakes are often more than five minutes apart (ADR-93).
var claudeAdmiralEnv = []string{"ENABLE_TOOL_SEARCH=false", "ENABLE_CLAUDEAI_MCP_SERVERS=false", "ENABLE_PROMPT_CACHING_1H=1"}

// writeAdmiralMCP writes <admiral dir>/.mcp.json for a copilot admiral and
// returns its path.
func writeAdmiralMCP(home, exe, adm string) (string, error) {
	dir := supervisor.DirOf(home, adm)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	cfg, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"fleet": map[string]any{
		"type": "local", "command": exe, "args": []string{"mcp", "--admiral", adm}, "tools": []string{"*"}}}}, "", "  ")
	path := filepath.Join(dir, ".mcp.json")
	return path, os.WriteFile(path, append(cfg, '\n'), 0o600)
}

// mcpCapable: the admiral gets fleet tools under this profile.
func mcpCapable(p tools.Profile) bool { return p.Patterns == "copilot" || p.Patterns == "claude" }

// parseUpArgs parses `fleet up [--no-attach] [--admiral-profile NAME]`.
func parseUpArgs(args []string) (noAttach bool, prof string, err error) {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	na := fs.Bool("no-attach", false, "start without attaching")
	ap := fs.String("admiral-profile", "", "tool profile for the admiral")
	sp := fs.String("supervisor-profile", "", "deprecated: use --admiral-profile")
	if err := fs.Parse(args); err != nil {
		return false, "", err
	}
	if fs.NArg() > 0 {
		return false, "", fmt.Errorf("usage: fleet up [--no-attach] [--admiral-profile NAME]")
	}
	if *sp != "" {
		fmt.Fprintln(os.Stderr, "fleet: --supervisor-profile is deprecated; use --admiral-profile")
		if *ap == "" {
			*ap = *sp
		}
	}
	return *na, *ap, nil
}

// topLevelProfile handles `fleet --admiral-profile NAME [up ...]`: the flag
// alone means up; with up it is passed on; any other command is an error.
func topLevelProfile(args []string) ([]string, error) {
	var fl []string
	for len(args) > 0 {
		name, val, eq := strings.Cut(args[0], "=")
		if name != "--admiral-profile" && name != "--supervisor-profile" {
			break
		}
		if !eq {
			if len(args) < 2 {
				return nil, fmt.Errorf("%s needs a profile name", name)
			}
			val, args = args[1], args[1:]
		}
		args = args[1:]
		fl = []string{name, val}
	}
	switch {
	case fl == nil:
		return args, nil
	case len(args) == 0:
		return append([]string{"up"}, fl...), nil
	case args[0] == "up":
		return append(append([]string{"up"}, fl...), args[1:]...), nil
	}
	return nil, fmt.Errorf("%s only applies to fleet up, not %q", fl[0], args[0])
}

// runningAdmiral is the profile the admiral runs under now.
func runningAdmiral(f *fleet.Fleet, s supervisor.Session) string {
	return runningAdmiralOf(f, supervisor.Main, s)
}

func runningAdmiralOf(f *fleet.Fleet, adm string, s supervisor.Session) string {
	if _, ok := f.Profiles[s.Profile]; ok {
		return s.Profile
	}
	return resolveAdmiralOf(f, adm)
}

// cmdAdmiralProfile: `fleet admiral profile [NAME | --pick]`.
func cmdAdmiralProfile(f *fleet.Fleet, args []string) error {
	adm, args, err := nameFlag(f, args)
	if err != nil {
		return err
	}
	cur := runningAdmiralOf(f, adm, supervisor.CurrentOf(f.Home, f.T, mainTarget(), adm))
	switch {
	case len(args) == 0:
		fmt.Print(profileList(f, cur))
		return nil
	case len(args) == 1 && args[0] == "--pick":
		return cmdAdmiralPick(f, adm, cur)
	case len(args) == 1 && !strings.HasPrefix(args[0], "-"):
		name := args[0]
		if p, ok := f.Profiles[name]; ok && !mcpCapable(p) {
			fmt.Fprintf(os.Stderr, "fleet: warning: profile %s (patterns %s) gets no fleet MCP tools as admiral\n", name, p.Patterns)
		}
		return runSwapOf(f, adm, name)
	}
	return fmt.Errorf("usage: fleet admiral profile [--name ADMIRAL] [NAME | --pick]")
}

// cmdAdmiralRestart: `fleet admiral restart`, a same-profile resume.
func cmdAdmiralRestart(f *fleet.Fleet, args []string) error {
	adm, args, err := nameFlag(f, args)
	if err != nil || len(args) > 0 {
		return fmt.Errorf("usage: fleet admiral restart [--name ADMIRAL]")
	}
	return runSwapOf(f, adm, runningAdmiralOf(f, adm, supervisor.CurrentOf(f.Home, f.T, mainTarget(), adm)))
}

// runSwap swaps and reports the result on stdout and the status line.
func runSwap(f *fleet.Fleet, name string) error { return runSwapOf(f, supervisor.Main, name) }

func runSwapOf(f *fleet.Fleet, adm, name string) error {
	msg, err := swapAdmiralOf(f, adm, name)
	if err != nil {
		f.T.Run("display-message", "-d", "8000", "-l", err.Error())
		return err
	}
	display(f, msg)
	fmt.Println(msg)
	return nil
}

// profileList prints the active profile and every profile, the active marked.
func profileList(f *fleet.Fleet, cur string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "admiral profile: %s\n", cur)
	for _, n := range tools.Names(f.Profiles) {
		mark := " "
		if n == cur {
			mark = "*"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, n)
	}
	return b.String()
}

// swapAdmiral moves the admiral to profile name (DESIGN §6.10): a resume
// restart for the same profile, else a handoff and a fresh session. If the
// new admiral dies within the start wait, the old one is restored and
// session.json and the last choice stay as they were.
func swapAdmiral(f *fleet.Fleet, name string) (string, error) {
	return swapAdmiralOf(f, supervisor.Main, name)
}

// swapAdmiralOf is swapAdmiral for admiral adm.
func swapAdmiralOf(f *fleet.Fleet, adm, name string) (string, error) {
	log := logx.For("admiral")
	unlock, err := supervisor.LockOf(f.Home, adm)
	if err != nil {
		return "", err
	}
	defer unlock()
	pane, err := supervisor.FindPaneOf(f.T, mainTarget(), adm)
	if err != nil {
		return "", err
	}
	old := supervisor.CurrentOf(f.Home, f.T, mainTarget(), adm)
	oldName := runningAdmiralOf(f, adm, old)
	np, ok := f.Profiles[name]
	if !ok {
		return "", fmt.Errorf("no profile %q; kept %s", name, oldName)
	}
	if name == oldName && len(np.Resume) > 0 && old.SessionID != "" {
		sl, err := admiralLaunchOf(f, adm, name, old.SessionID, "")
		if err != nil {
			return "", err
		}
		// Written only if the resume fails and a fresh admiral takes over.
		fallback := swapHandoff(oldName, name, old.SessionID, resumeCommand(np, f.Exe, old.SessionID),
			keptHandoffOf(f, adm, old), capturedTail(f, pane), time.Now())
		if err := startAdmiralOf(f, adm, pane, sl, old, oldName, false, fallback); err != nil {
			return "", err
		}
		ns := old
		ns.PaneID, ns.Profile = pane, name
		if ns.Generation == 0 {
			ns.Generation = 1
		}
		if ns.Started.IsZero() {
			ns.Started = time.Now()
		}
		if err := supervisor.SaveOf(f.Home, adm, ns); err != nil {
			return "", err
		}
		saveChoiceOf(f, adm, name)
		log.Info("restart", "profile", name, "session_id", old.SessionID, "pane", pane)
		return fmt.Sprintf("admiral restarted on %s (resumed %s)", name, shortID(old.SessionID)), nil
	}
	kept := keptHandoffOf(f, adm, old)
	tail := ""
	if kept == "" {
		tail = capturedTail(f, pane)
	}
	text := swapHandoff(oldName, name, old.SessionID, resumeCommand(f.Profiles[oldName], f.Exe, old.SessionID), kept, tail, time.Now())
	if err := supervisor.WriteHandoffOf(f.Home, adm, text); err != nil {
		return "", err
	}
	sl, err := admiralLaunchOf(f, adm, name, "", supervisor.BootPrompt)
	if err != nil {
		return "", err
	}
	if err := startAdmiralOf(f, adm, pane, sl, old, oldName, true, ""); err != nil {
		return "", err
	}
	gen := old.Generation + 1
	if gen < 2 {
		gen = 2
	}
	ns := supervisor.Session{SessionID: sl.ID, Profile: name, PaneID: pane, Started: time.Now(), Dir: sl.Dir, Generation: gen, Previous: old.SessionID}
	if err := supervisor.SaveOf(f.Home, adm, ns); err != nil {
		return "", err
	}
	saveChoiceOf(f, adm, name)
	pasteBoot(f, pane, sl)
	log.Info("swap", "from", oldName, "to", name, "generation", gen, "session_id", sl.ID, "previous", old.SessionID, "kept_handoff", kept != "")
	return fmt.Sprintf("%s → %s · gen %d (handoff written)", admiralLabel(adm), name, gen), nil
}

// swapPrefix starts every automatic swap handoff.
const swapPrefix = "Admiral profile swap at"

// keptHandoff returns the handoff the old admiral wrote during its run, if
// any. An automatic swap handoff (a failed earlier swap) is not kept: the pane
// tail is fresher.
func keptHandoff(f *fleet.Fleet, old supervisor.Session) string {
	return keptHandoffOf(f, supervisor.Main, old)
}

func keptHandoffOf(f *fleet.Fleet, adm string, old supervisor.Session) string {
	h, at, ok := supervisor.ReadHandoffOf(f.Home, adm)
	if !ok || old.Started.IsZero() || at.Before(old.Started) || strings.HasPrefix(h, swapPrefix) {
		return ""
	}
	return h
}

// capturedTail is the last swapTailLines lines of the admiral pane.
func capturedTail(f *fleet.Fleet, pane string) string {
	out, _ := f.T.Capture(pane, swapTailLines)
	return paneTail(out, swapTailLines)
}

// startAdmiral respawns pane with sl and waits out the start window. If the
// new admiral dies, the old one is restored (see restoreAdmiral) and an error
// returned. resumeOK says a resume of the old session may be tried; it is false
// when sl itself was that resume. fallback, if set, is written as the handoff
// before a fresh old-profile admiral starts.
func startAdmiral(f *fleet.Fleet, pane string, sl supLaunch, old supervisor.Session, oldName string, resumeOK bool, fallback string) error {
	return startAdmiralOf(f, supervisor.Main, pane, sl, old, oldName, resumeOK, fallback)
}

func startAdmiralOf(f *fleet.Fleet, adm, pane string, sl supLaunch, old supervisor.Session, oldName string, resumeOK bool, fallback string) error {
	if err := respawnAdmiral(f, pane, sl); err != nil {
		return err
	}
	setAdmiralPaneOptsOf(f, adm, pane, sl.Profile)
	display(f, "admiral → "+sl.Profile+": starting…")
	if !paneDied(f, pane, startWait()) {
		return nil
	}
	err := fmt.Errorf("admiral profile %s failed to start; kept %s", sl.Profile, oldName)
	if !resumeOK {
		err = fmt.Errorf("admiral %s could not resume %s; started a fresh %s admiral", sl.Profile, shortID(old.SessionID), oldName)
	}
	logx.For("admiral").Error("start failed; restoring", "profile", sl.Profile, "old", oldName, "old_session", old.SessionID)
	if rerr := restoreAdmiralOf(f, adm, pane, old, oldName, resumeOK, fallback); rerr != nil {
		logx.For("admiral").Error("restore failed", "err", rerr)
		return fmt.Errorf("%v (restore failed: %v)", err, rerr)
	}
	return err
}

// restoreAdmiral brings back the old admiral: first a resume of its session
// when allowed and the profile can, and if that dies too (or was not
// possible) a fresh old-profile admiral with the boot prompt, saved in
// session.json as a new generation. The persisted choice is left alone.
func restoreAdmiral(f *fleet.Fleet, pane string, old supervisor.Session, oldName string, resumeOK bool, fallback string) error {
	return restoreAdmiralOf(f, supervisor.Main, pane, old, oldName, resumeOK, fallback)
}

func restoreAdmiralOf(f *fleet.Fleet, adm, pane string, old supervisor.Session, oldName string, resumeOK bool, fallback string) error {
	log := logx.For("admiral")
	p, ok := f.Profiles[oldName]
	if !ok {
		return fmt.Errorf("no profile %q", oldName)
	}
	if resumeOK && len(p.Resume) > 0 && old.SessionID != "" {
		sl, err := admiralLaunchOf(f, adm, oldName, old.SessionID, "")
		if err != nil {
			return err
		}
		if err := respawnAdmiral(f, pane, sl); err != nil {
			return err
		}
		setAdmiralPaneOptsOf(f, adm, pane, oldName)
		if !paneDied(f, pane, startWait()) {
			return nil
		}
		log.Error("resume restore died; starting fresh", "profile", oldName, "session_id", old.SessionID)
	}
	if fallback != "" {
		if err := supervisor.WriteHandoffOf(f.Home, adm, fallback); err != nil {
			log.Warn("write fallback handoff", "err", err)
		}
	}
	sl, err := admiralLaunchOf(f, adm, oldName, "", supervisor.BootPrompt)
	if err != nil {
		return err
	}
	if err := respawnAdmiral(f, pane, sl); err != nil {
		return err
	}
	setAdmiralPaneOptsOf(f, adm, pane, oldName)
	gen := old.Generation + 1
	if gen < 2 {
		gen = 2
	}
	ns := supervisor.Session{SessionID: sl.ID, Profile: oldName, PaneID: pane, Started: time.Now(), Dir: sl.Dir, Generation: gen, Previous: old.SessionID}
	if err := supervisor.SaveOf(f.Home, adm, ns); err != nil {
		return err
	}
	pasteBoot(f, pane, sl)
	if paneDied(f, pane, startWait()) {
		return fmt.Errorf("fresh %s admiral died too", oldName)
	}
	log.Info("restored fresh", "profile", oldName, "session_id", sl.ID, "generation", gen)
	return nil
}

// respawnAdmiral replaces the admiral pane's process with sl.
func respawnAdmiral(f *fleet.Fleet, pane string, sl supLaunch) error {
	a := []string{"respawn-pane", "-k", "-c", sl.Dir}
	for _, e := range sl.Env {
		a = append(a, "-e", e)
	}
	_, err := f.T.Run(append(a, "-t", pane, sl.Cmd)...)
	return err
}

// setAdmiralPaneOpts tags the admiral pane and clears the old context meter.
func setAdmiralPaneOpts(f *fleet.Fleet, pane, prof string) {
	setAdmiralPaneOptsOf(f, supervisor.Main, pane, prof)
}

// setAdmiralPaneOptsOf also tags a non-main admiral's pane (@fleet_admiral)
// and sets the name and colour its border shows once there are several.
func setAdmiralPaneOptsOf(f *fleet.Fleet, adm, pane, prof string) {
	f.T.SetPaneOption(pane, "@role", "sup")
	if adm != supervisor.Main {
		f.T.SetPaneOption(pane, "@fleet_admiral", adm)
	}
	f.T.SetPaneOption(pane, "@fleet_admiral_name", adm)
	f.T.SetPaneOption(pane, "@fleet_admiral_color", theme.AdmiralColor(supervisor.Slot(f.Home, adm)))
	f.T.SetPaneOption(pane, "remain-on-exit", "on")
	if prof != "" {
		f.T.SetPaneOption(pane, "@fleet_admiral_profile", prof)
	}
	for _, o := range []string{"@fleet_ctx", "@fleet_ctx_color"} {
		f.T.UnsetPaneOption(pane, o)
	}
}

// pasteBoot pastes the boot prompt when the profile could not take it in argv.
func pasteBoot(f *fleet.Fleet, pane string, sl supLaunch) {
	if sl.Paste == "" {
		return
	}
	p := f.Profiles[sl.Profile]
	wait := bootPasteTimeout
	if wait == 0 {
		wait = f.Prefs.For(sl.Profile).ReadyTimeout
	}
	f.PastePrompt(pane, p, sl.Paste, wait)
}

func saveChoice(f *fleet.Fleet, name string) { saveChoiceOf(f, supervisor.Main, name) }

func saveChoiceOf(f *fleet.Fleet, adm, name string) {
	if adm != supervisor.Main {
		if err := supervisor.SetProfile(f.Home, adm, name); err != nil {
			logx.For("admiral").Warn("save admiral profile", "admiral", adm, "err", err)
		}
	}
	if err := supervisor.SaveChoiceOf(f.Home, adm, name); err != nil {
		logx.For("admiral").Warn("save profile choice", "err", err)
	}
}

// startWait is how long a new admiral must stay alive: 5 s, or
// FLEET_ADMIRAL_START_WAIT (a Go duration such as "300ms").
func startWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("FLEET_ADMIRAL_START_WAIT")); err == nil && d >= 0 {
		return d
	}
	return 5 * time.Second
}

// paneDied polls #{pane_dead} until wait has passed.
func paneDied(f *fleet.Fleet, pane string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		out, err := f.T.Run("display-message", "-p", "-t", pane, "#{pane_dead}")
		if err != nil || out == "1" {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// paneTail is the last n lines of a capture, trailing blank lines dropped.
func paneTail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// resumeCommand is the shell command that reopens session id under p
// ("" when p can't resume or there is no id).
func resumeCommand(p tools.Profile, exe, id string) string {
	if len(p.Resume) == 0 || id == "" {
		return ""
	}
	argv, _ := p.Argv(tools.Vars{Fleet: exe, Name: "admiral", ID: id}, tools.Launch{Resume: true})
	var parts []string
	keys := make([]string, 0, len(p.Env))
	for k := range p.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"="+tools.Quote(p.Env[k]))
	}
	for _, a := range argv {
		parts = append(parts, tools.Quote(a))
	}
	return strings.Join(parts, " ")
}

// swapHandoff is the automatic handoff for a profile swap: a header line,
// then the handoff the old admiral wrote (kept) or the tail of its pane,
// cut so the whole stays inside the handoff cap.
func swapHandoff(from, to, prevID, resume, kept, tail string, now time.Time) string {
	if prevID == "" {
		prevID = "unknown"
	}
	if resume == "" {
		resume = "n/a"
	}
	head := fmt.Sprintf(swapPrefix+" %s: %s → %s. Previous session: %s (resume: %s).",
		now.Format("2006-01-02 15:04"), from, to, prevID, resume)
	body, fromTail := kept, false
	if body == "" && tail != "" {
		body, fromTail = "Last lines of the previous admiral's pane:\n"+tail, true
	}
	if body == "" {
		return head
	}
	limit := handoffCap - len(head) - 3 // "\n\n" and WriteHandoff's final "\n"
	if limit < 0 {
		limit = 0
	}
	if len(body) > limit {
		if fromTail {
			i := len(body) - limit
			for i < len(body) && !utf8.RuneStart(body[i]) {
				i++
			}
			body = body[i:]
		} else {
			i := limit
			for i > 0 && !utf8.RuneStart(body[i]) {
				i--
			}
			body = body[:i]
		}
	}
	return head + "\n\n" + body
}

func shortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	if id == "" {
		return "?"
	}
	return id
}

// upSwap: `fleet up --admiral-profile NAME` while fleet runs under another
// profile swaps the admiral before attaching. Same profile: just remember it.
func upSwap(f *fleet.Fleet, name string) {
	if runningAdmiral(f, supervisor.Current(f.Home, f.T, mainTarget())) == name {
		saveChoice(f, name)
		return
	}
	if err := runSwap(f, name); err != nil {
		fmt.Fprintln(os.Stderr, "fleet:", err)
	}
}
