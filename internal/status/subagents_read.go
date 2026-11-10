package status

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// ScanSubagents applies the complete lines in data to prev and returns the new
// state (list pruned) and how many bytes it consumed (up to and including the
// last '\n'; a partial last line is left for the next call).
func ScanSubagents(data []byte, prev Subs, now time.Time) (Subs, int64) {
	st := prev.clone()
	var off int
	for {
		n := bytes.IndexByte(data[off:], '\n')
		if n < 0 {
			break
		}
		st = applyLine(st, data[off:off+n], now)
		off += n + 1
	}
	st.List = PruneSubagents(st.List, now)
	return st, int64(off)
}

// ReadSubagents reads the event log at path from offset to EOF and applies it
// to prev. A file shorter than offset was replaced: it is read from 0 with an
// empty state. A missing file leaves (prev, offset) unchanged.
func ReadSubagents(path string, offset int64, prev Subs, now time.Time) (Subs, int64) {
	return readSubagents(path, offset, prev, now, applyLine)
}

// ReadACPSubagents is ReadSubagents over fleet's transcript of an ACP agent
// (~/.fleet/acp/<name>.jsonl), whose tool entries name the subagent a call
// starts (agy's invoke_subagent; DESIGN §7d).
func ReadACPSubagents(path string, offset int64, prev Subs, now time.Time) (Subs, int64) {
	return readSubagents(path, offset, prev, now, applyACPLine)
}

func readSubagents(path string, offset int64, prev Subs, now time.Time, applyLine func(Subs, []byte, time.Time) Subs) (Subs, int64) {
	log := logx.For("status")
	f, err := os.Open(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Warn("subagent log", "path", path, "err", err)
		}
		return prev, offset
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		log.Warn("subagent log", "path", path, "err", err)
		return prev, offset
	}
	if fi.Size() < offset {
		offset, prev = 0, Subs{}
	}
	st := prev.clone()
	if fi.Size() == offset {
		st.List = PruneSubagents(st.List, now)
		return st, offset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		log.Warn("subagent log", "path", path, "err", err)
		return prev, offset
	}
	// ReadBytes grows past the buffer, so multi-MB lines are fine (a
	// bufio.Scanner would stop at its 64K default).
	r := bufio.NewReaderSize(f, 256<<10)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Warn("subagent log", "path", path, "err", err)
			}
			break // a partial last line waits for the next poll
		}
		offset += int64(len(line))
		st = applyLine(st, line[:len(line)-1], now)
	}
	st.List = PruneSubagents(st.List, now)
	return st, offset
}

// applyACPLine applies one transcript entry: a subagent's tool call, or a
// new session or finished turn that ends whatever still runs.
func applyACPLine(st Subs, line []byte, now time.Time) Subs {
	if !bytes.Contains(line, []byte(`"sub":"`)) && !bytes.Contains(line, []byte(`"kind":"session"`)) && !bytes.Contains(line, []byte(`"kind":"status"`)) {
		return st
	}
	var e struct {
		At     time.Time `json:"at"`
		Kind   string    `json:"kind"`
		Status string    `json:"status"`
		ToolID string    `json:"tool_id"`
		Sub    string    `json:"sub"`
	}
	if json.Unmarshal(line, &e) != nil {
		return st
	}
	ts := e.At
	if ts.IsZero() {
		ts = now
	}
	list := st.List
	switch {
	case e.Kind == "session", e.Kind == "status" && (e.Status == string(Idle) || e.Status == string(Error)):
		list = endRunning(list, ts)
	case e.Kind == "tool" && e.Sub != "" && e.ToolID != "":
		i := -1
		for j := range list {
			if list[j].ID == e.ToolID {
				i = j
			}
		}
		switch e.Status {
		case "completed", "failed":
			if i >= 0 && list[i].State == SubRunning {
				list[i].State, list[i].Ended = SubDone, ts
				if e.Status == "failed" {
					list[i].State = SubFailed
				}
			}
		default:
			if i < 0 {
				list = append(list, Subagent{ID: e.ToolID, Name: e.Sub, State: SubRunning, Started: ts})
			}
		}
	}
	st.List = list
	return st
}
