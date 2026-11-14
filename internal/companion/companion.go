// Package companion is the shared contract for the CLIs fleet reads or
// writes (cass, gh, rsync and ssh, the summary CLI, the clipboard, a
// terminal; ADR-126). An adapter declares a Spec; Find resolves the binary
// (environment, then [integrations.<name>] path, then PATH); Run and JSON
// call it with a timeout and the first stderr line on failure. Fleet never
// installs a companion: a missing one turns its feature off with a hint.
package companion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Spec is what an adapter declares about the tool it calls.
type Spec struct {
	Name    string   // "cass": the [integrations.<name>] key and the doctor label
	Bins    []string // names tried on PATH in order; empty means {Name}
	Env     []string // overrides, first set wins: a path, or "off"
	Version []string // argv after the binary that prints the version, e.g. {"--version"}
	Min     string   // oldest version the adapter is tested against; "" = any
	Feature string   // what is off without it, for doctor: "session search"
	Hint    string   // one line on how to install it; fleet never installs
}

// Errors Find returns, wrapped in *Error.
var (
	ErrMissing = errors.New("not installed")
	ErrOff     = errors.New("off by config")
	ErrTooOld  = errors.New("too old")
)

// Error says which companion is unusable and why.
type Error struct {
	Spec    Spec
	Err     error  // ErrMissing, ErrOff or ErrTooOld
	Version string // the version found, for ErrTooOld
}

func (e *Error) Error() string {
	switch e.Err {
	case ErrTooOld:
		return fmt.Sprintf("%s %s is older than %s", e.Spec.Name, e.Version, e.Spec.Min)
	case ErrOff:
		return e.Spec.Name + " is off by config"
	}
	return e.Spec.Name + " is not installed"
}

func (e *Error) Unwrap() error { return e.Err }

// Tool is a resolved companion binary.
type Tool struct {
	Spec   Spec
	Path   string
	Source string // where the path came from: "$FLEET_CASS", "config", "PATH"
}

// Config is the [integrations.<name>] path lookup ("" = not set, "auto" =
// PATH, "off", or a path). internal/prefs sets it; nil means no config.
var Config func(name string) string

var (
	regMu    sync.Mutex
	registry = map[string]Spec{}
)

// Register adds s to the list fleet doctor reports on and returns it.
func Register(s Spec) Spec {
	regMu.Lock()
	defer regMu.Unlock()
	registry[s.Name] = s
	return s
}

// Registered lists every registered Spec by name.
func Registered() []Spec {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]Spec, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LookPath is exec.LookPath; tests swap it.
var LookPath = exec.LookPath

// Find resolves s's binary: the first set Env variable, then the
// [integrations.<name>] path, then the first of Bins on PATH. A Min makes
// it probe the version (cached by path and mtime) and refuse an older one.
func Find(s Spec) (Tool, error) {
	t, err := locate(s)
	if err != nil {
		return t, err
	}
	if s.Min != "" && len(s.Version) > 0 {
		if v := probe(t.Path, s.Version); v != "" && Older(v, s.Min) {
			return t, &Error{Spec: s, Err: ErrTooOld, Version: v}
		}
	}
	return t, nil
}

func locate(s Spec) (Tool, error) {
	for _, env := range s.Env {
		v := strings.TrimSpace(os.Getenv(env))
		if v == "" {
			continue
		}
		if v == "off" {
			return Tool{Spec: s, Source: "$" + env}, &Error{Spec: s, Err: ErrOff}
		}
		return resolved(s, v, "$"+env)
	}
	if Config != nil {
		switch v := strings.TrimSpace(Config(s.Name)); v {
		case "", "auto":
		case "off":
			return Tool{Spec: s, Source: "config"}, &Error{Spec: s, Err: ErrOff}
		default:
			return resolved(s, v, "config")
		}
	}
	bins := s.Bins
	if len(bins) == 0 {
		bins = []string{s.Name}
	}
	for _, b := range bins {
		if p, err := LookPath(b); err == nil {
			return Tool{Spec: s, Path: p, Source: "PATH"}, nil
		}
	}
	return Tool{Spec: s}, &Error{Spec: s, Err: ErrMissing}
}

// resolved accepts an explicit path or bare name; one that cannot be found
// is missing rather than an exec error later.
func resolved(s Spec, v, src string) (Tool, error) {
	p, err := LookPath(v)
	if err != nil {
		return Tool{Spec: s, Path: v, Source: src}, &Error{Spec: s, Err: ErrMissing}
	}
	return Tool{Spec: s, Path: p, Source: src}, nil
}

// Opts tunes one call.
type Opts struct {
	Timeout time.Duration // 0 = DefaultTimeout
	Low     bool          // run under nice -n 19 and ionice -c3 when present
	Env     []string      // extra KEY=value entries on top of the process env
	Dir     string
}

// DefaultTimeout bounds a call whose Opts give none.
const DefaultTimeout = 20 * time.Second

// Runner runs one call; tests swap it to serve fixtures instead of exec.
var Runner = execRun

// Run runs the tool with no stdin and a timeout; the error names the tool
// and subcommand and carries the first stderr line.
func (t Tool) Run(ctx context.Context, o Opts, args ...string) ([]byte, error) {
	out, err := Runner(ctx, t.Path, args, o)
	if err != nil {
		return out, fmt.Errorf("%s%s: %w", t.Spec.Name, sub(args), err)
	}
	return out, nil
}

// JSON runs the tool and decodes stdout into v, ignoring unknown fields.
func (t Tool) JSON(ctx context.Context, o Opts, v any, args ...string) error {
	out, err := t.Run(ctx, o, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("%s%s: unreadable output: %v", t.Spec.Name, sub(args), err)
	}
	return nil
}

func sub(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return " " + args[0]
}

func execRun(ctx context.Context, path string, args []string, o Opts) ([]byte, error) {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := append([]string{path}, args...)
	if o.Low {
		if p, err := exec.LookPath("ionice"); err == nil {
			argv = append([]string{p, "-c3"}, argv...)
		}
		if p, err := exec.LookPath("nice"); err == nil {
			argv = append([]string{p, "-n", "19"}, argv...)
		}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = o.Dir
	if len(o.Env) > 0 {
		cmd.Env = append(os.Environ(), o.Env...)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if l := FirstLine(stderr.String()); l != "" {
			return out, fmt.Errorf("%v: %s", err, l)
		}
		return out, err
	}
	return out, nil
}

// FirstLine is the first non-blank line of s, trimmed.
func FirstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

type probeKey struct {
	path  string
	mtime time.Time
}

var (
	probeMu    sync.Mutex
	probeCache = map[probeKey]string{}
	versionRe  = regexp.MustCompile(`\d+(\.\d+)+`)
)

// Version is the version t reports through its Spec's Version argv, ""
// when it has none or does not say. Cached by path and mtime.
func (t Tool) Version() string {
	if t.Path == "" || len(t.Spec.Version) == 0 {
		return ""
	}
	return probe(t.Path, t.Spec.Version)
}

func probe(path string, argv []string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	key := probeKey{path, st.ModTime()}
	probeMu.Lock()
	v, ok := probeCache[key]
	probeMu.Unlock()
	if ok {
		return v
	}
	out, _ := Runner(context.Background(), path, argv, Opts{Timeout: 5 * time.Second})
	v = versionRe.FindString(string(out))
	probeMu.Lock()
	probeCache[key] = v
	probeMu.Unlock()
	return v
}

// Older reports whether dotted version a is below b.
func Older(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// Status is one companion's state, for fleet doctor.
type Status struct {
	Spec    Spec
	Path    string
	Source  string
	Version string
	Err     error // nil, or an *Error
}

// Check resolves s and probes its version, for fleet doctor.
func Check(s Spec) Status {
	t, err := Find(s)
	st := Status{Spec: s, Path: t.Path, Source: t.Source, Err: err}
	if err == nil {
		st.Version = t.Version()
		return st
	}
	var e *Error
	if errors.As(err, &e) {
		st.Version = e.Version
	}
	return st
}
