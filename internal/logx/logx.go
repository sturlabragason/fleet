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

// Init opens the log file. Safe to call more than once.
func Init() {
	once.Do(func() {
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
