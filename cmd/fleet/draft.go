package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sessions"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// Replacing an admiral's pane (reset, handoff, profile swap) kills whatever
// the user had typed but not submitted there (ADR-140), and a message the
// user submitted while the restart was held (ADR-178). holdForDraft defers
// the replacement while the input box holds text; bootPrompt hands a sent
// message and the old session's unanswered ones to the new admiral; restoreDraft saves the text and puts unsent
// text back in the new admiral's box.
var (
	draftWait        = 2 * time.Minute        // how long a restart waits for the user
	draftPoll        = 250 * time.Millisecond // how often it looks; short, so little of a sent message's end is missed
	draftSettle      = 3 * time.Second        // how long an emptied box may take to show the admiral busy
	draftRestoreWait = 30 * time.Second       // how long the new admiral's box may take to show
)

// draft is the user's text in the admiral's input box when a restart came.
type draft struct {
	Text    string // the last text seen ("" = the box was empty from the start)
	Pending bool   // still in the box at the deadline
	Sent    bool   // submitted to the old admiral during the hold: the restart kills that turn
	Carried bool   // the restarted admiral was given the sent message
	// Unanswered are messages the user had already submitted that the old
	// session never saw, e.g. one queued while the admiral was calling
	// fleet_handoff (ADR-196). UnansweredFile holds them when they are too
	// long for the launch command.
	Unanswered     []string
	UnansweredFile string
}

// maxUnanswered is how many messages a boot prompt carries; maxInline is
// the most the "## Last user messages" section may add to the launch
// command, which tmux refuses at about 16 KB.
const (
	maxUnanswered = 5
	maxInline     = 2048
)

// withUnanswered adds the user's unseen messages in admiral session id to
// d, without fleet's own wake lines and boot prompts. Too long for the
// launch command, they go to a file the boot prompt names.
func withUnanswered(f *fleet.Fleet, adm, id string, d draft) draft {
	found := sessions.Lookup(f.Profiles, id, "")
	if len(found) != 1 {
		return d
	}
	var out []string
	for _, m := range sessions.Unanswered(found[0].Path) {
		if !strings.HasPrefix(m, "[fleet]") && !strings.HasPrefix(m, supervisor.BootPrompt) {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return d
	}
	d.Unanswered = out[max(len(out)-maxUnanswered, 0):]
	log := logx.For("admiral")
	// Quoting for the shell and tmux turns each ' into up to 17 bytes.
	if m := unansweredMessage(d.Unanswered, ""); len(m)+16*strings.Count(m, "'") > maxInline {
		dir := filepath.Join(f.Home, "drafts")
		path := filepath.Join(dir, adm+"-unanswered-"+time.Now().Format("20060102-150405")+".md")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Warn("save unanswered", "err", err)
		} else if err := os.WriteFile(path, []byte(m+"\n"), 0o600); err != nil {
			log.Warn("save unanswered", "err", err)
		} else {
			d.UnansweredFile = path
		}
		if d.UnansweredFile == "" {
			d.Unanswered = nil // never inline past what tmux takes
		}
	}
	log.Info("unanswered messages carried", "admiral", adm, "session_id", id, "n", len(d.Unanswered), "file", d.UnansweredFile)
	return d
}

// holdForDraft waits up to draftWait for admiral adm's input box to empty,
// telling the user once. When the box empties it decides whether the user
// sent the text (the admiral turned busy within draftSettle) or cleared it.
func holdForDraft(f *fleet.Fleet, adm string) draft {
	last := f.AdmiralDraft(adm)
	if last == "" {
		return draft{}
	}
	logx.For("admiral").Info("restart held for unsubmitted text", "admiral", adm, "wait", draftWait)
	draftNotice(f, "10000", admiralLabel(adm)+" restart waits for your unsubmitted text: press Enter or clear it (max "+draftWait.String()+"; after that it moves to the new admiral)")
	for end := time.Now().Add(draftWait); time.Now().Before(end); {
		time.Sleep(draftPoll)
		d := f.AdmiralDraft(adm)
		if d == "" {
			return draft{Text: last, Sent: sentToAdmiral(f, adm)}
		}
		last = d
	}
	return draft{Text: last, Pending: true}
}

// sentToAdmiral reports whether admiral adm shows busy within draftSettle:
// Enter starts or queues a turn, clearing the box does not.
func sentToAdmiral(f *fleet.Fleet, adm string) bool {
	for end := time.Now().Add(draftSettle); ; time.Sleep(draftPoll) {
		if f.AdmiralBusy(adm) {
			return true
		}
		if !time.Now().Before(end) {
			return false
		}
	}
}

// sentMessage asks the restarted admiral to answer a message the user sent
// during the hold, since the restart killed the turn that would have
// answered it ("" = nothing was sent).
func sentMessage(d draft) string {
	if !d.Sent {
		return ""
	}
	return "The user sent this message just before the admiral restart, so it was never answered (the last few characters may be missing). Answer it first:\n\n" + d.Text
}

// unansweredMessage asks the restarted admiral to answer the messages the
// old session never saw, inline or in file ("" = none).
func unansweredMessage(msgs []string, file string) string {
	if len(msgs) == 0 {
		return ""
	}
	head := "## Last user messages\n\nThe user sent these to the previous admiral and it was replaced before it saw them. Answer them first, oldest first"
	if file != "" {
		return head + ". They are in " + file + "; read it."
	}
	var b strings.Builder
	b.WriteString(head + ":")
	for _, m := range msgs {
		b.WriteString("\n\n> " + strings.ReplaceAll(m, "\n", "\n> "))
	}
	return b.String()
}

// bootPrompt is a fresh admiral's first prompt, with the unanswered and
// sent messages. A sent message that is already the newest unanswered one
// is not repeated.
func bootPrompt(d draft) string {
	parts := []string{supervisor.BootPrompt}
	if m := unansweredMessage(d.Unanswered, d.UnansweredFile); m != "" {
		parts = append(parts, m)
	}
	n := len(d.Unanswered)
	if m := sentMessage(d); m != "" && (n == 0 || !strings.HasPrefix(d.Unanswered[n-1], d.Text)) {
		parts = append(parts, m)
	}
	return strings.Join(parts, "\n\n")
}

// restoreDraft saves the text under ~/.fleet/drafts and, when the user left
// it in the box (pending), types it into the new admiral's input box.
func restoreDraft(f *fleet.Fleet, adm string, d draft) {
	if d.Text == "" {
		if len(d.Unanswered) > 0 {
			draftNotice(f, "15000", fmt.Sprintf("%d unanswered message(s) to the old %s were passed to the new one", len(d.Unanswered), admiralLabel(adm)))
		}
		return
	}
	log := logx.For("admiral")
	dir := filepath.Join(f.Home, "drafts")
	path := filepath.Join(dir, adm+"-"+time.Now().Format("20060102-150405")+".txt")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Warn("save draft", "err", err)
	} else if err := os.WriteFile(path, []byte(d.Text+"\n"), 0o600); err != nil {
		log.Warn("save draft", "err", err)
	}
	restored := d.Pending && f.RestoreAdmiralDraft(adm, d.Text, draftRestoreWait, draftPoll)
	msg := "your text from before the " + admiralLabel(adm) + " restart is saved in " + path
	switch {
	case restored:
		msg = "your unsubmitted text is back in the " + admiralLabel(adm) + "'s input box (copy: " + path + ")"
	case d.Carried:
		msg = "the message you sent as the " + admiralLabel(adm) + " restarted was passed on to be answered (copy: " + path + ")"
	case d.Sent:
		msg = "the message you sent as the " + admiralLabel(adm) + " restarted was not answered; it is saved in " + path
	}
	log.Info("draft kept", "admiral", adm, "path", path, "restored", restored, "sent", d.Sent, "carried", d.Carried)
	draftNotice(f, "15000", msg)
}

// draftNotice shows msg on the status line for ms milliseconds.
func draftNotice(f *fleet.Fleet, ms, msg string) {
	if _, err := f.T.Run("display-message", "-d", ms, "-l", msg); err != nil {
		logx.For("admiral").Warn("draft notice", "err", err)
	}
}
