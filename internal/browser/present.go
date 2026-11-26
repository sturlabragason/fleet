package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
)

// PwDump is the PipeWire graph dump the presenting auto-detect reads.
var PwDump = companion.Register(companion.Spec{
	Name:    "pw-dump",
	Feature: "auto-detect screen sharing and calls for the browser guard (fleet present still works)",
	Hint:    "ships with PipeWire (pipewire-utils on Fedora, pipewire-bin on Debian)",
})

func presentFile(home string) string { return filepath.Join(home, "present") }
func blockedFile(home string) string { return filepath.Join(home, "browser-blocked.jsonl") }

// Presenting returns why the user counts as presenting, or "": the manual
// `fleet present on`, else what detect finds.
func Presenting(ctx context.Context, home string, detect Detector) string {
	if _, err := os.Stat(presentFile(home)); err == nil {
		return "fleet present on"
	}
	if detect == nil {
		return ""
	}
	return detect(ctx)
}

// SetPresent turns the manual override on or off. Off leaves auto-detect.
func SetPresent(home string, on bool) error {
	if on {
		return os.WriteFile(presentFile(home), []byte(time.Now().Format(time.RFC3339)+"\n"), 0o600)
	}
	if err := os.Remove(presentFile(home)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("present off: %w", err)
	}
	return nil
}

// Detect reads the PipeWire graph; no pw-dump means no auto-detect.
func Detect(ctx context.Context) string {
	t, err := companion.Find(PwDump)
	if err != nil {
		return ""
	}
	out, err := t.Run(ctx, companion.Opts{Timeout: 3 * time.Second})
	if err != nil {
		return ""
	}
	return Signal(out)
}

var (
	// screenRe: the video node a screen share creates (GNOME's
	// gnome-shell-screencast, wlroots' xdpw, KDE's kwin, the portal).
	screenRe = regexp.MustCompile(`(?i)screencast|screen.?share|xdpw|xdg-desktop-portal|kwin`)
	// callRe: an app that, capturing the mic or camera, is in a call.
	// A browser counts: Meet runs in one.
	callRe = regexp.MustCompile(`(?i)teams|zoom|meet|webex|slack|discord|skype|brave|chrom|firefox|electron`)
)

type pwObject struct {
	Type string `json:"type"`
	Info *struct {
		State string         `json:"state"`
		Props map[string]any `json:"props"`
	} `json:"info"`
}

// Signal finds a presenting signal in pw-dump's JSON, or "": a screen
// share's video node in any state, or a call app's running mic or camera
// capture stream. A webcam alone or an idle call app is not presenting.
func Signal(dump []byte) string {
	var objs []pwObject
	if json.Unmarshal(dump, &objs) != nil {
		return ""
	}
	for _, o := range objs {
		if o.Type != "PipeWire:Interface:Node" || o.Info == nil {
			continue
		}
		p := func(k string) string { s, _ := o.Info.Props[k].(string); return s }
		class := p("media.class")
		if strings.Contains(class, "Video") && screenRe.MatchString(p("node.name")+" "+p("media.name")+" "+p("node.description")) {
			return "screen share (" + p("node.name") + ")"
		}
		app := p("application.name") + " " + p("application.process.binary")
		if strings.HasPrefix(class, "Stream/Input/") && o.Info.State == "running" && callRe.MatchString(app) {
			return "call (" + strings.TrimSpace(app) + ")"
		}
	}
	return ""
}

// Blocked is one refused open, kept until it is handed over.
type Blocked struct {
	Time   time.Time `json:"time"`
	Agent  string    `json:"agent"`
	URL    string    `json:"url"`
	Reason string    `json:"reason"`
}

// Record appends a refused open to the blocked log.
func Record(home string, b Blocked) error {
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("blocked open: %w", err)
	}
	f, err := os.OpenFile(blockedFile(home), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("blocked open log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("blocked open log: %w", err)
	}
	return nil
}

// Pending reads the blocked opens not yet handed over.
func Pending(home string) ([]Blocked, error) { return readBlocked(blockedFile(home)) }

func readBlocked(path string) ([]Blocked, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("blocked open log: %w", err)
	}
	defer f.Close()
	var out []Blocked
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var b Blocked
		if json.Unmarshal(sc.Bytes(), &b) == nil {
			out = append(out, b)
		}
	}
	return out, sc.Err()
}

// Status is the present state and the blocked opens, for `fleet present`
// and the admiral's fleet_wall present actions. Not presenting, it hands
// the blocked URLs over and clears the log; while presenting it gives only
// their count, since the screen is shared.
func Status(ctx context.Context, home string, detect Detector) (string, error) {
	pending, err := Pending(home)
	if err != nil {
		return "", err
	}
	if why := Presenting(ctx, home, detect); why != "" {
		return fmt.Sprintf("presenting (%s): every browser open is blocked; %d blocked so far, listed once presenting ends", why, len(pending)), nil
	}
	var b strings.Builder
	b.WriteString("not presenting: agents open pages in the default browser, except never-open agents")
	if len(pending) == 0 {
		return b.String(), nil
	}
	// Move the log aside before reading it, so an open blocked meanwhile
	// starts a new log instead of being deleted unseen.
	taken := blockedFile(home) + ".handover"
	if err := os.Rename(blockedFile(home), taken); err != nil {
		return "", fmt.Errorf("hand over blocked opens: %w", err)
	}
	if pending, err = readBlocked(taken); err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "\nblocked opens to hand over (%d):", len(pending))
	for _, p := range pending {
		fmt.Fprintf(&b, "\n  %s %s %s (%s)", p.Time.Format("15:04"), p.Agent, p.URL, p.Reason)
	}
	if err := os.Remove(taken); err != nil {
		return b.String(), fmt.Errorf("clear blocked open log: %w", err)
	}
	return b.String(), nil
}
