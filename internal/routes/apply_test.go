package routes

import (
	"slices"
	"testing"
)

func TestFill(t *testing.T) {
	r := Route{Name: "dream", Dir: "/hub", Profile: "copilot", Effort: "standard", Worktree: "false",
		Interactive: "true", May: []string{"merge-local"}, Skill: "skill-builder", Prompt: "Rules.\n\n{task}"}
	yes := true
	tests := []struct {
		name string
		in   Spawn
		want Spawn
	}{
		{"unset filled", Spawn{Prompt: "go"}, Spawn{Dir: "/hub", Profile: "copilot", Effort: "standard",
			May: []string{"merge-local"}, Prompt: "Use the skill-builder skill for this task.\n\nRules.\n\ngo"}},
		{"explicit wins", Spawn{Dir: "/x", Effort: "pro", Worktree: &yes, May: []string{}},
			Spawn{Dir: "/x", Profile: "copilot", Effort: "pro", May: []string{},
				Prompt: "Use the skill-builder skill for this task.\n\nRules."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in
			r.Fill(&got)
			if got.Dir != tt.want.Dir || got.Profile != tt.want.Profile || got.Effort != tt.want.Effort ||
				!slices.Equal(got.May, tt.want.May) || got.Prompt != tt.want.Prompt {
				t.Errorf("Fill = %+v, want %+v", got, tt.want)
			}
			if wantWT := tt.in.Worktree == nil; wantWT && (got.Worktree == nil || *got.Worktree) {
				t.Errorf("worktree = %v, want false from the route", got.Worktree)
			}
			if got.Interactive == nil || !*got.Interactive {
				t.Errorf("interactive = %v, want true from the route", got.Interactive)
			}
		})
	}
}
