package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// Replacing an admiral's pane (reset, handoff, profile swap) kills whatever
// the user had typed but not submitted there (ADR-140), and a message the
// user submitted while the restart was held (ADR-178). holdForDraft defers
// the replacement while the input box holds text; bootPrompt hands a sent
// message to the new admiral; restoreDraft saves the text and puts unsent
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

// bootPrompt is a fresh admiral's first prompt, with the sent message.
func bootPrompt(d draft) string {
	if m := sentMessage(d); m != "" {
		return supervisor.BootPrompt + "\n\n" + m
	}
	return supervisor.BootPrompt
}

// restoreDraft saves the text under ~/.fleet/drafts and, when the user left
// it in the box (pending), types it into the new admiral's input box.
func restoreDraft(f *fleet.Fleet, adm string, d draft) {
	if d.Text == "" {
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
