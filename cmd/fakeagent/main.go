// Command fakeagent prints agent-like output and cycles through states, or
// with --acp serves a scripted Agent Client Protocol session (DESIGN §7d).
package main

import (
	"os"

	"github.com/sturlabragason/fleet/internal/fake"
)

func main() {
	os.Exit(fake.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
