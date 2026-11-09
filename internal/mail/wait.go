package mail

import (
	"os"
	"time"
)

// Stamp is a cheap change marker for a mailbox: its inbox file's size and
// modification time. fleet wait stats it every second and reads the box
// only when it changed (ADR-72).
type Stamp struct {
	Size int64
	Mod  time.Time
}

// StampOf returns box's Stamp; a box with no inbox file yet has the zero one.
func (s *Store) StampOf(box string) Stamp {
	if checkBox(box) != nil {
		return Stamp{}
	}
	fi, err := os.Stat(s.inboxPath(box))
	if err != nil {
		return Stamp{}
	}
	return Stamp{fi.Size(), fi.ModTime()}
}

// Box returns every message in box, read or not, oldest first. It marks
// nothing read.
func (s *Store) Box(box string) []Msg {
	if checkBox(box) != nil {
		return nil
	}
	return s.read(box)
}
