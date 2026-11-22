package burn

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
)

// TaskSpec declares the task CLI companion binary used to read +burn tasks.
var TaskSpec = companion.Register(companion.Spec{
	Name:    "task",
	Bins:    []string{"task"},
	Env:     []string{"FLEET_TASK"},
	Feature: "read +burn tasks for fleet burn",
	Hint:    "install the task CLI or set FLEET_TASK",
})

// ReadTasks queries the task companion for pending items matching +tag and
// returns them sorted by urgency descending.
func ReadTasks(ctx context.Context, tag string) ([]Task, error) {
	tool, err := companion.Find(TaskSpec)
	if err != nil {
		return nil, fmt.Errorf("find task companion: %w", err)
	}

	tag = strings.TrimPrefix(strings.TrimSpace(tag), "+")
	if tag == "" {
		tag = "burn"
	}

	out, err := tool.Run(ctx, companion.Opts{Timeout: 15 * time.Second},
		"rc.confirmation=off",
		"rc.verbose=nothing",
		"+"+tag,
		"status:pending",
		"export",
	)
	if err != nil {
		return nil, fmt.Errorf("export tasks: %w", err)
	}

	return ParseTasksJSON(out)
}

// ParseTasksJSON decodes a task JSON export, filters out tasks with
// empty descriptions, normalizes effort, and sorts by urgency descending
// (tie-breaking by UUID ascending).
func ParseTasksJSON(data []byte) ([]Task, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var raw []Task
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, fmt.Errorf("parse tasks json: %w", err)
	}

	tasks := make([]Task, 0, len(raw))
	for _, t := range raw {
		if strings.TrimSpace(t.Description) == "" {
			continue
		}
		t.Effort = t.NormalizedEffort()
		tasks = append(tasks, t)
	}

	slices.SortFunc(tasks, func(a, b Task) int {
		if a.Urgency != b.Urgency {
			return cmp.Compare(b.Urgency, a.Urgency)
		}
		return cmp.Compare(a.UUID, b.UUID)
	})
	return tasks, nil
}
