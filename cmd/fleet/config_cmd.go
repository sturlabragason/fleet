package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/prefs"
)

// configExplain is `fleet config explain`: the settings a spawn of a profile
// gets, each with the layer that set it, and the FLEET_* variables set in
// this environment.
type configExplain struct {
	Profile  string          `json:"profile"`
	Config   string          `json:"config"`
	Settings []prefs.Setting `json:"settings"`
	Env      []string        `json:"env"`
}

func cmdConfig(f *fleet.Fleet, args []string) error {
	const usage = "usage: fleet config explain [--json] [profile]"
	if len(args) == 0 || args[0] != "explain" {
		return errors.New(usage)
	}
	fs := flag.NewFlagSet("config explain", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New(usage)
	}
	profile := f.Prefs.DefaultProfile()
	if fs.NArg() == 1 {
		profile = fs.Arg(0)
	}
	if _, ok := f.Profiles[profile]; !ok {
		return fmt.Errorf("unknown profile %q (fleet profiles lists them)", profile)
	}
	out := configExplain{Profile: profile, Config: prefs.Path(), Env: fleetEnv(os.Environ())}
	out.Settings = append([]prefs.Setting{f.ExplainDriver(profile)}, f.Prefs.Explain(profile)...)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Printf("profile %s, config %s\n", out.Profile, out.Config)
	for _, s := range out.Settings {
		fmt.Printf("  %-16s %-24s %s\n", s.Key, s.Value, s.Source)
	}
	if len(out.Env) > 0 {
		fmt.Println("environment:")
		for _, e := range out.Env {
			fmt.Println("  " + e)
		}
	}
	return nil
}

// secretName reports whether an env name looks like it holds a credential.
func secretName(k string) bool {
	for _, w := range []string{"TOKEN", "SECRET", "KEY", "PASS"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// fleetEnv is the FLEET_* variables of env, sorted, with credential
// values hidden.
func fleetEnv(env []string) []string {
	out := []string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if !strings.HasPrefix(k, "FLEET_") {
			continue
		}
		if secretName(k) {
			v = "(set)"
		}
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
