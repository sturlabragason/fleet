package status

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Background jobs (ADR-217). A Claude Code agent can end its turn while a
// shell it started with run_in_background, or an MCP call Claude moved to
// the background, still runs; Claude wakes it with a task-notification when
// the job ends. No hook fires for either end, but the session transcript
// records both, so the Stop hook reads it and fleet keeps the agent working
// instead of calling it stopped without a report.

var (
	bgMarks  = [][]byte{[]byte(`"backgroundTaskId":"`), []byte(`moved to the background as task `), []byte(`<task-notification>`), []byte(`"task_type":"`)}
	bgMCPID  = regexp.MustCompile(`moved to the background as task ([A-Za-z0-9_-]+)`)
	bgTaskID = regexp.MustCompile(`<task-id>([^<]+)</task-id>`)
)

// BackgroundMax bounds a wait on background jobs (ADR-217): a launch older
// than this when the turn ends is not counted, nor is a subagent started
// before it, and an agent waits at most this long after its turn ended. A
// forgotten server or a lost end must not hide a missing report for good.
const BackgroundMax = 30 * time.Minute

// bgLine is the part of a transcript line ScanBackground reads.
type bgLine struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Operation string          `json:"operation"`
	Content   string          `json:"content"`
	Result    json.RawMessage `json:"toolUseResult"`
}

// ScanBackground folds transcript lines into running, the ids of background
// jobs still running: a background shell or backgrounded MCP call adds its id;
// a queued task-notification or a TaskStop result removes it. Only the tool's
// own records count (a launch in a "user" tool-result line, a notification in
// an "enqueue" queue-operation), so the same words quoted in a command's
// output or an API request copy change nothing. A launch stamped before since
// is ignored.
func ScanBackground(data []byte, running []string, since time.Time) []string {
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		if !slices.ContainsFunc(bgMarks, func(m []byte) bool { return bytes.Contains(line, m) }) {
			continue
		}
		var e bgLine
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch {
		case e.Type == "queue-operation" && e.Operation == "enqueue" && strings.HasPrefix(e.Content, "<task-notification>"):
			for _, m := range bgTaskID.FindAllStringSubmatch(e.Content, -1) {
				running = slices.DeleteFunc(running, func(id string) bool { return id == m[1] })
			}
		case e.Type == "user":
			running = bgResult(e.Result, running, e.Timestamp.Before(since))
		}
	}
	return running
}

// bgResult applies one tool result: a background shell's or a backgrounded
// MCP call's launch, unless old, or a TaskStop.
func bgResult(raw json.RawMessage, running []string, old bool) []string {
	var obj struct {
		BackgroundTaskID string `json:"backgroundTaskId"`
		TaskID           string `json:"task_id"`
		TaskType         string `json:"task_type"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		switch {
		case obj.TaskType != "" && obj.TaskID != "":
			return slices.DeleteFunc(running, func(id string) bool { return id == obj.TaskID })
		case obj.BackgroundTaskID != "" && !old:
			return addID(running, obj.BackgroundTaskID)
		}
		return running
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if old || json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 || !strings.HasPrefix(blocks[0].Text, `MCP tool "`) {
		return running
	}
	if m := bgMCPID.FindStringSubmatch(blocks[0].Text); m != nil {
		return addID(running, m[1])
	}
	return running
}

func addID(ids []string, id string) []string {
	if slices.Contains(ids, id) {
		return ids
	}
	return append(ids, id)
}

// BackgroundAt reads the whole transcript at path and returns the background
// jobs launched within BackgroundMax of now and still running, and the offset
// StillBackground reads on from: the end of the last complete line, so a line
// Claude is still writing is read again later. Anything unreadable is no jobs.
func BackgroundAt(path string, now time.Time) ([]string, int64) {
	if path == "" {
		return nil, 0
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0
	}
	b = b[:bytes.LastIndexByte(b, '\n')+1]
	return ScanBackground(b, nil, now.Add(-BackgroundMax)), int64(len(b))
}

// StillBackground returns which of s's background jobs are still running:
// those with no end recorded in the transcript after s.TranscriptOff.
func StillBackground(s State) []string {
	if len(s.Background) == 0 {
		return nil
	}
	fh, err := os.Open(s.Transcript)
	if err != nil {
		return nil // the transcript is gone: nothing to wait on
	}
	defer fh.Close()
	b, err := io.ReadAll(io.NewSectionReader(fh, s.TranscriptOff, 1<<62))
	if err != nil {
		return nil
	}
	return ScanBackground(b, slices.Clone(s.Background), time.Time{})
}

// RecentRunning counts the running subagents started within BackgroundMax of
// now: one whose stop was missed must not hold its agent working for good.
func RecentRunning(list []Subagent, now time.Time) int {
	n := 0
	for _, s := range list {
		if s.State == SubRunning && now.Sub(s.Started) < BackgroundMax {
			n++
		}
	}
	return n
}
