// Package logx sets up slog to ~/.fleet/logs/fleet.log and gives per-component loggers.
package logx

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

var (
	once sync.Once
	base *slog.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
)

// Home returns the fleet state dir (~/.fleet or $FLEET_HOME).
func Home() string {
	if h := os.Getenv("FLEET_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".fleet")
}

// Init makes the fleet home private, opens the log file and makes it slog's
// default, so a bare slog call never writes to the terminal a TUI draws on.
// Safe to call more than once.
func Init() {
	once.Do(func() {
		defer func() { slog.SetDefault(base) }()
		privateHome(Home())
		dir := filepath.Join(Home(), "logs")
		_ = os.MkdirAll(dir, 0o755)
		f, err := openRotator(filepath.Join(dir, "fleet.log"))
		if err != nil {
			return
		}
		base = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})).With("pid", os.Getpid())
	})
}

// For returns a logger tagged with the component name.
func For(component string) *slog.Logger { return base.With("component", component) }

// privateHome creates home as 0700, or drops group and other access from an
// existing one, so mail, notes and pane captures under it stay with the
// user (ADR-162). A home the user doesn't own is left as it is.
func privateHome(home string) {
	_ = os.MkdirAll(home, 0o700)
	if fi, err := os.Stat(home); err == nil && fi.IsDir() && fi.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(home, fi.Mode().Perm()&^0o077)
	}
}
