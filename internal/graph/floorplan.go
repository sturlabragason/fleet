package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/logx"
)

// FloorplanSpec is the floorplan companion (ADR-207): a command that takes
// <repo root> <graph out dir> and leaves a floorplan in <out>/floorplan
// (index.html and query.mjs). scripts/fleet-floorplan is one, wrapping
// haydenschmitty/floorplan; any repo works, and one with
// .fleet/floorplan/architecture.json gets its own map instead of a generated one.
var FloorplanSpec = companion.Register(companion.Spec{
	Name:    "floorplan",
	Bins:    []string{"fleet-floorplan"},
	Env:     []string{"FLEET_FLOORPLAN"},
	Feature: "code floorplan beside the graph (fleet graph)",
	Hint:    "link fleet's scripts/fleet-floorplan onto PATH and clone a vetted haydenschmitty/floorplan to ~/.local/share/floorplan",
})

const floorplanTime = 5 * time.Minute

// HasFloorplan says whether a floorplan agents can query sits in out.
func HasFloorplan(out string) bool { return exists(FloorplanQuery(out)) }

// FloorplanQuery is the floorplan's lookup CLI in a graph out dir.
func FloorplanQuery(out string) string { return filepath.Join(out, "floorplan", "query.mjs") }

// buildFloorplan runs the floorplan companion after a good graph build. It
// is optional and never fails the build: without the companion it does
// nothing, and a failed run keeps the previous floorplan.
func buildFloorplan(ctx context.Context, root, out string) {
	tool, err := companion.Find(FloorplanSpec)
	if err != nil {
		return
	}
	log := logx.For("graphify")
	start := time.Now()
	if _, err := tool.Run(ctx, companion.Opts{Timeout: floorplanTime, Low: true, Clean: true, Env: floorplanEnv(out)}, root, out); err != nil {
		log.Warn("floorplan build failed", "root", root, "err", err)
		return
	}
	log.Info("floorplan built", "root", root, "secs", time.Since(start).Seconds())
}

// floorplanEnv is the run's environment on top of the clean one: the graph
// out dir and the user's FLOORPLAN_* settings (checkout, vetted commit).
func floorplanEnv(out string) []string {
	env := []string{"GRAPHIFY_OUT=" + out}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "FLOORPLAN_") {
			env = append(env, kv)
		}
	}
	return env
}
