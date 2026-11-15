package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
)

// Replacing an admiral's pane (reset, handoff, profile swap) kills whatever
// the user had typed but not submitted there (ADR-140). holdForDraft defers
// the replacement while the input box holds text; restoreDraft saves it and
// puts it back in the new admiral's box.
var (
	draftWait        = 2 * time.Minute  // how long a restart waits for the user
	draftPoll        = time.Second      // how often it looks
	draftRestoreWait = 30 * time.Second // how long the new admiral's box may take to show
)

// holdForDraft waits up to draftWait for admiral adm's input box to empty,
// telling the user once. It returns the last text seen and whether it is
// still there ("" = the box was empty from the start).
func holdForDraft(f *fleet.Fleet, adm string) (string, bool) {
	last := f.AdmiralDraft(adm)
	if last == "" {
		return "", false
	}
	logx.For("admiral").Info("restart held for unsubmitted text", "admiral", adm, "wait", draftWait)
	draftNotice(f, "10000", admiralLabel(adm)+" restart waits for your unsubmitted text: press Enter or clear it (max "+draftWait.String()+"; after that it moves to the new admiral)")
	for end := time.Now().Add(draftWait); time.Now().Before(end); {
		time.Sleep(draftPoll)
		d := f.AdmiralDraft(adm)
		if d == "" {
			return last, false
		}
		last = d
	}
	return last, true
}

// restoreDraft saves draft under ~/.fleet/drafts and, when the user left it
// in the box (pending), types it into the new admiral's input box.
func restoreDraft(f *fleet.Fleet, adm, draft string, pending bool) {
	if draft == "" {
		return
	}
	log := logx.For("admiral")
	dir := filepath.Join(f.Home, "drafts")
	path := filepath.Join(dir, adm+"-"+time.Now().Format("20060102-150405")+".txt")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Warn("save draft", "err", err)
	} else if err := os.WriteFile(path, []byte(draft+"\n"), 0o600); err != nil {
		log.Warn("save draft", "err", err)
	}
	restored := pending && f.RestoreAdmiralDraft(adm, draft, draftRestoreWait, draftPoll)
	msg := "your text from before the " + admiralLabel(adm) + " restart is saved in " + path
	if restored {
		msg = "your unsubmitted text is back in the " + admiralLabel(adm) + "'s input box (copy: " + path + ")"
	}
	log.Info("draft kept", "admiral", adm, "path", path, "restored", restored)
	draftNotice(f, "15000", msg)
}

// draftNotice shows msg on the status line for ms milliseconds.
func draftNotice(f *fleet.Fleet, ms, msg string) {
	if _, err := f.T.Run("display-message", "-d", ms, "-l", msg); err != nil {
		logx.For("admiral").Warn("draft notice", "err", err)
	}
}
