package fake

import (
	"flag"
	"fmt"
	"io"
)

// Main runs the fake agent with command-line args (after the program name)
// and returns the exit code. Without --acp it is exactly the TUI fake:
// `fakeagent [name]`. With --acp it serves ACP on in/out:
// `fakeagent [name] --acp [--resume] [--no-load] [--state DIR]`, flags in
// any position.
func Main(args []string, in io.Reader, out, errw io.Writer) int {
	isACP := false
	for _, a := range args {
		if a == "--acp" || a == "-acp" {
			isACP = true
		}
	}
	if !isACP {
		name := "fake"
		if len(args) > 0 {
			name = args[0]
		}
		Run(name, in, out)
		return 0
	}
	fs := flag.NewFlagSet("fakeagent", flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.Bool("acp", false, "serve the Agent Client Protocol on stdin/stdout")
	resume := fs.Bool("resume", false, "offer session/resume")
	noLoad := fs.Bool("no-load", false, "do not offer session/load")
	state := fs.String("state", DefaultStateDir(), "directory sessions persist in")
	name, rest := "fake", args
	var pos []string
	for {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) > 0 {
		name = pos[0]
	}
	if err := ServeACP(in, out, ACPOptions{Name: name, Resume: *resume, NoLoad: *noLoad, StateDir: *state}); err != nil {
		fmt.Fprintln(errw, "fakeagent:", err)
		return 1
	}
	return 0
}
