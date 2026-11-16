//go:build extexample

package mcp

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

// The public example extension (DESIGN §15 "Extensions"): it fills every
// seam of this package so public CI compiles and tests them. Its tool
// answers on any server; the gate, suffix and brief stay inert for a
// *Server until a test turns them on with exampleOn.
type exampleState struct{ calls atomic.Int32 }

var exampleStates sync.Map // *Server -> *exampleState

func exampleOn(s *Server) *exampleState {
	v, _ := exampleStates.LoadOrStore(s, &exampleState{})
	return v.(*exampleState)
}

func exampleOf(s *Server) *exampleState {
	if v, ok := exampleStates.Load(s); ok {
		return v.(*exampleState)
	}
	return nil
}

const exampleRow = "| Example extension | `fleet_example` |\n"

func init() {
	Register(Extension{
		Name: "example",
		Tools: []tool{{"fleet_example", "The example extension's tool: echoes name.",
			obj([]string{"name"}, map[string]any{"name": str("any text")})}},
		Call: func(s *Server, name string, a args) (string, error) {
			return "example: " + a.Name, nil
		},
		IdentityBound: []string{"fleet_example"},
		Parity:        map[string]string{"example": "fleet_example"},
		Instructions: func(text string) string {
			const after = "| Admirals |"
			if i := strings.Index(text, after); i >= 0 {
				j := i + strings.Index(text[i:], "\n") + 1
				return text[:j] + exampleRow + text[j:]
			}
			return text + "\n" + exampleRow
		},
		Gate: func(s *Server, name string, a args) error {
			if exampleOf(s) != nil && a.Name == "example-refused" {
				return errors.New("example: refused")
			}
			return nil
		},
		OnCall: func(s *Server) {
			if st := exampleOf(s); st != nil {
				st.calls.Add(1)
			}
		},
		After: func(s *Server, name string, a args, out string) string {
			if exampleOf(s) != nil && name == "fleet_example" && a.Name == "after" {
				return "\n[example after: " + out + "]"
			}
			return ""
		},
		Suffix: func(s *Server) string {
			if exampleOf(s) != nil {
				return "\n[example suffix]"
			}
			return ""
		},
		Brief: func(s *Server) string {
			if exampleOf(s) != nil {
				return "## Example\nexample brief\n\n"
			}
			return ""
		},
	})
}
