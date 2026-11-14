//go:build extexample

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/sturlabragason/fleet/internal/fleet"
)

// The public example extension (DESIGN §15 "Extensions"): it fills every
// seam of the CLI so public CI compiles and tests them. Its checks refuse
// only for a home a test turns on with exampleRefuse; Open and UI record
// the *Fleet they ran for.
var example struct {
	mu     sync.Mutex
	refuse map[string]bool // home -> refuse
	opened []*fleet.Fleet
	ui     []*fleet.Fleet
	args   []string // of the last fleet example
}

func exampleRefuse(home string) {
	example.mu.Lock()
	defer example.mu.Unlock()
	if example.refuse == nil {
		example.refuse = map[string]bool{}
	}
	example.refuse[home] = true
}

func exampleRefused(home string) bool {
	example.mu.Lock()
	defer example.mu.Unlock()
	return example.refuse[home]
}

const exampleUsage = "  fleet example [args]                      the example extension's command (build tag extexample)\n"

func init() {
	register(extension{
		Name: "example",
		Commands: map[string]func([]string) error{"example": func(args []string) error {
			example.mu.Lock()
			example.args = args
			example.mu.Unlock()
			fmt.Println("example", strings.Join(args, " "))
			return nil
		}},
		Usage: func(u string) string {
			const at = "\n  fleet doctor [--json]"
			if i := strings.Index(u, at); i >= 0 {
				return u[:i+1] + exampleUsage + u[i+1:]
			}
			return u + "\n" + exampleUsage
		},
		Before: func(home, cmd string) error {
			if exampleRefused(home) {
				return errors.New("example: refused " + cmd)
			}
			return nil
		},
		Open: func(f *fleet.Fleet) {
			example.mu.Lock()
			example.opened = append(example.opened, f)
			example.mu.Unlock()
		},
		MCP: func(f *fleet.Fleet, admiral, agent string) error {
			if exampleRefused(f.Home) {
				return errors.New("example: no MCP server for " + admiral + "/" + agent)
			}
			return nil
		},
		Tmux: func(f *fleet.Fleet) [][]string {
			return [][]string{{"set-option", "-g", "@fleet_example", f.Exe}}
		},
		UI: func(ctx context.Context, f *fleet.Fleet) {
			example.mu.Lock()
			example.ui = append(example.ui, f)
			example.mu.Unlock()
			<-ctx.Done()
		},
	})
}
