package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tools"
)

// cmdModels is `fleet models [--json] [profile...]` (ADR-112): with no
// profile, each profile's model support without starting any tool; with
// profiles, the models each one's tool offers, asked over ACP.
func cmdModels(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	names, probe := fs.Args(), fs.NArg() > 0
	if !probe {
		names = tools.Names(f.Profiles)
	}
	var out []fleet.ProfileModels
	for _, n := range names {
		get := f.ModelSupport
		if probe {
			get = f.ProfileModels
		}
		pm, err := get(n)
		if err != nil {
			return err
		}
		out = append(out, pm)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	for _, pm := range out {
		fmt.Print(formatModels(pm, probe))
	}
	if !probe {
		fmt.Println("\nfleet models <profile> asks the tool for the models it offers (starts it once, no prompt, no tokens)")
	}
	return nil
}

func formatModels(pm fleet.ProfileModels, probed bool) string {
	var b strings.Builder
	def := pm.Default
	if def == "" {
		def = "the tool's own"
	}
	sel := "no"
	if pm.Selectable {
		sel = "yes"
	}
	if !probed {
		fmt.Fprintf(&b, "%-16s select=%-3s default=%s", pm.Profile, sel, def)
		if pm.Note != "" {
			fmt.Fprintf(&b, "  (%s)", pm.Note)
		}
		return b.String() + "\n"
	}
	fmt.Fprintf(&b, "%s: select=%s default=%s", pm.Profile, sel, def)
	if pm.Listed {
		fmt.Fprintf(&b, " tool-current=%s", pm.Current)
	}
	b.WriteString("\n")
	if pm.Note != "" {
		fmt.Fprintf(&b, "  %s\n", pm.Note)
	}
	for _, m := range pm.Models {
		if m.Name != "" && m.Name != m.ModelID {
			fmt.Fprintf(&b, "  %-28s %s\n", m.ModelID, m.Name)
		} else {
			fmt.Fprintf(&b, "  %s\n", m.ModelID)
		}
	}
	return b.String()
}
