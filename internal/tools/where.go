package tools

import "strings"

// Where is where an agent of this profile runs under the acp driver: "" on
// this machine, "cluster:<context>" for the kagent adapter (ADR-145, ADR-150),
// "cluster" when the profile names no context (kubectl's current one). It is
// read from the adapter the profile runs, never from its name, so every
// kagent profile gets it; a later remote driver adds its own kind here.
func (p Profile) Where() string {
	c := p.ACPCommand
	if len(c) < 3 || c[1] != "kagent" || c[2] != "acp" {
		return ""
	}
	if ctx := flagValue(append(c[3:len(c):len(c)], p.ACP...), "context"); ctx != "" {
		return "cluster:" + ctx
	}
	return "cluster"
}

// flagValue is the last value of Go-style flag name in args (-name V,
// --name V, -name=V, --name=V); "" when it is not there.
func flagValue(args []string, name string) string {
	v := ""
	for i, a := range args {
		k, val, eq := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || k != name {
			continue
		}
		switch {
		case eq:
			v = val
		case i+1 < len(args):
			v = args[i+1]
		}
	}
	return v
}
