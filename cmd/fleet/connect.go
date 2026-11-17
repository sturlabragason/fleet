package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// `fleet connect claude-desktop` (ADR-159): put fleet into Claude Desktop's
// claude_desktop_config.json as a local stdio MCP server that serves a
// pane-less external admiral, or take it out again.

const (
	desktopServer   = "fleet"   // the mcpServers key fleet owns
	desktopAdmiral  = "desktop" // the default external admiral name
	connectUsage    = "usage: fleet connect claude-desktop [--name ADMIRAL] [--config PATH] [--print | --dry-run] [--remove]"
	desktopRestart  = "quit Claude Desktop completely (not just the window) and open it again"
	desktopFileName = "claude_desktop_config.json"
)

// desktopEnvKeys are the fleet settings a Desktop-started server must share
// with the user's shell, besides PATH.
var desktopEnvKeys = []string{"FLEET_HOME", "FLEET_CONFIG", "XDG_CONFIG_HOME"}

// claudeDesktopConfig is where Claude Desktop keeps its config on goos;
// Linux has only unofficial builds, which use ~/.config/Claude.
func claudeDesktopConfig(goos, home string, getenv func(string) string) (string, error) {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", desktopFileName), nil
	case "windows":
		if ad := getenv("APPDATA"); ad != "" {
			return filepath.Join(ad, "Claude", desktopFileName), nil
		}
		return "", errors.New("APPDATA is not set")
	}
	dir := getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "Claude", desktopFileName), nil
}

// desktopEntry is fleet's mcpServers entry: an absolute command, the
// external admiral flag, and a PATH that finds tmux (Desktop starts servers
// with a minimal PATH, and fleet runs plain `tmux`).
func desktopEntry(exe, admiral, path string, getenv func(string) string) map[string]any {
	env := map[string]any{"PATH": path}
	for _, k := range desktopEnvKeys {
		if v := getenv(k); v != "" {
			env[k] = v
		}
	}
	return map[string]any{"command": exe, "args": []any{"mcp", "--admiral", admiral}, "env": env}
}

// desktopPath is the user's PATH with tmux's directory in it.
func desktopPath(path, tmuxDir string) string {
	if tmuxDir == "" || slices.Contains(filepath.SplitList(path), tmuxDir) {
		return path
	}
	if path == "" {
		return tmuxDir
	}
	return tmuxDir + string(os.PathListSeparator) + path
}

// mergeDesktop sets (entry != nil) or removes fleet's server in a Desktop
// config, keeping every other key and server. changed is false when there
// was nothing to remove.
func mergeDesktop(raw []byte, entry map[string]any) (out []byte, changed bool, err error) {
	cfg := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, false, fmt.Errorf("not valid JSON, left alone: %w", err)
		}
	}
	servers, ok := cfg["mcpServers"].(map[string]any)
	if cfg["mcpServers"] != nil && !ok {
		return nil, false, errors.New(`"mcpServers" is not an object, left alone`)
	}
	if servers == nil {
		servers = map[string]any{}
	}
	if entry == nil {
		if _, had := servers[desktopServer]; !had {
			return raw, false, nil
		}
		delete(servers, desktopServer)
	} else {
		servers[desktopServer] = entry
	}
	cfg["mcpServers"] = servers
	out, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encode config: %w", err)
	}
	return append(out, '\n'), true, nil
}

// writeDesktop backs up the current file (if any) beside it, then replaces
// it atomically with its old mode. It returns the backup path.
func writeDesktop(path string, data []byte, now time.Time) (string, error) {
	mode := fs.FileMode(0o600)
	backup := ""
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if st, serr := os.Stat(path); serr == nil {
			mode = st.Mode().Perm()
		}
		if backup, err = writeNew(path+".fleet-backup-"+now.Format("20060102-150405"), old, mode); err != nil {
			return "", fmt.Errorf("back up %s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	tmp := path + ".fleet-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return "", fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("replace %s: %w", path, err)
	}
	return backup, nil
}

// writeNew writes data to base, or base.1, base.2, ... when it exists, so a
// second run in the same second never overwrites the first backup.
func writeNew(base string, data []byte, mode fs.FileMode) (string, error) {
	for i := range 100 {
		p := base
		if i > 0 {
			p = fmt.Sprintf("%s.%d", base, i)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return "", err
		}
		return p, f.Close()
	}
	return "", fmt.Errorf("%s: too many backups", base)
}

type connectOpts struct {
	name, config  string
	print, remove bool
}

func parseConnect(args []string) (connectOpts, error) {
	fl := flag.NewFlagSet("connect", flag.ContinueOnError)
	var o connectOpts
	fl.StringVar(&o.name, "name", desktopAdmiral, "external admiral the Desktop server serves")
	fl.StringVar(&o.config, "config", "", "Claude Desktop config file (default: the OS's location)")
	fl.BoolVar(&o.print, "print", false, "print the config fleet would write; change nothing")
	dry := fl.Bool("dry-run", false, "same as --print")
	fl.BoolVar(&o.remove, "remove", false, "take fleet out of the config (the admiral stays registered)")
	// Positionals may sit between flags; flag stops at the first one, so
	// collect it and parse on (reorderFlags can't tell bool flags apart).
	var pos []string
	for {
		if err := fl.Parse(args); err != nil {
			return o, errors.New(connectUsage)
		}
		if fl.NArg() == 0 {
			break
		}
		pos = append(pos, fl.Arg(0))
		args = fl.Args()[1:]
	}
	if len(pos) != 1 || pos[0] != "claude-desktop" {
		return o, errors.New(connectUsage)
	}
	o.print = o.print || *dry
	return o, nil
}

func cmdConnect(f *fleet.Fleet, args []string) error {
	o, err := parseConnect(args)
	if err != nil {
		return err
	}
	if o.config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home dir: %w", err)
		}
		if o.config, err = claudeDesktopConfig(runtime.GOOS, home, os.Getenv); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Dir(o.config)); err != nil && !o.print {
			return fmt.Errorf("no Claude Desktop config dir at %s (install Claude Desktop and open it once, or pass --config PATH)", filepath.Dir(o.config))
		}
	}
	raw, err := os.ReadFile(o.config)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", o.config, err)
	}
	var entry map[string]any
	if !o.remove {
		if entry, err = connectEntry(f, o.name); err != nil {
			return err
		}
	}
	out, changed, err := mergeDesktop(raw, entry)
	if err != nil {
		return fmt.Errorf("%s: %w", o.config, err)
	}
	if o.print {
		fmt.Fprintf(os.Stderr, "fleet: would write %s:\n", o.config)
		fmt.Print(string(out))
		return nil
	}
	if !changed {
		fmt.Printf("fleet is not in %s; nothing to remove\n", o.config)
		return nil
	}
	if !o.remove {
		if err := ensureExternal(f, o.name); err != nil {
			return err
		}
	}
	backup, err := writeDesktop(o.config, out, time.Now())
	if err != nil {
		return err
	}
	if backup != "" {
		backup = "; backup " + backup
	}
	if o.remove {
		fmt.Printf("fleet removed from %s%s. %s. Admiral %s stays registered: fleet admiral close %s hands its agents to main.\n", o.config, backup, desktopRestart, o.name, o.name)
		return nil
	}
	fmt.Printf("fleet added to %s as admiral %s%s. %s, then ask it for fleet_status.\n", o.config, o.name, backup, desktopRestart)
	return nil
}

// connectEntry builds the entry for this fleet binary and the user's PATH.
func connectEntry(f *fleet.Fleet, name string) (map[string]any, error) {
	if err := supervisor.ValidName(name); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(f.Exe) {
		return nil, fmt.Errorf("fleet binary path %q is not absolute", f.Exe)
	}
	tm, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("tmux is not on PATH; fleet needs it: %w", err)
	}
	return desktopEntry(f.Exe, name, desktopPath(os.Getenv("PATH"), filepath.Dir(tm)), os.Getenv), nil
}

// ensureExternal registers name as an external admiral unless it already
// is one; a tmux admiral of that name is an error, not a takeover.
func ensureExternal(f *fleet.Fleet, name string) error {
	if a, ok := supervisor.Find(f.Home, name); ok {
		if !a.External {
			return fmt.Errorf("admiral %s already runs in tmux; pick another --name", name)
		}
		return nil
	}
	if _, err := supervisor.AddExternal(f.Home, name, f.Prefs.AdmiralMax()); err != nil {
		return fmt.Errorf("register admiral %s: %w", name, err)
	}
	return nil
}

// desktopConnected reports whether a Desktop config has fleet's server, for
// fleet doctor: found is false when the config is missing or unreadable.
func desktopConnected(path string) (found, connected bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return true, false
	}
	_, ok := cfg.MCPServers[desktopServer]
	return true, ok
}

// checkDesktop is fleet doctor's Claude Desktop line: info only, and only
// when Desktop's config dir exists.
func checkDesktop(userHome string, getenv func(string) string) (doctorCheck, bool) {
	path, err := claudeDesktopConfig(runtime.GOOS, userHome, getenv)
	if err != nil {
		return doctorCheck{}, false
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return doctorCheck{}, false
	}
	c := doctorCheck{Name: "claude desktop", Level: levelInfo}
	switch found, ok := desktopConnected(path); {
	case ok:
		c.Level, c.Detail = levelOK, "fleet is in "+path
	case found:
		c.Detail, c.Fix = "installed, fleet not connected", "fleet connect claude-desktop"
	default:
		c.Detail, c.Fix = "config dir found, no config yet", "fleet connect claude-desktop"
	}
	return c, true
}
