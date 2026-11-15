package worktree

import (
	"context"
	"strings"
	"testing"
)

// Touching leaves out a worktree whose work is in main by patch id and
// counts it; all brings it back, marked merged. Live work stays.
func TestTouchingLeavesOutMergedWorktrees(t *testing.T) {
	repo := patchRepo(t)
	ws, _, left, err := Touching(context.Background(), repo, repo, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Branch != "fleet/x" || ws[0].Merged || left != 1 {
		t.Fatalf("got %+v, left %d; want only fleet/x, 1 left out", ws, left)
	}
	if txt := TouchingText(ws, false, left); !strings.Contains(txt, "1 merged worktrees left out; fleet worktrees --prune removes them") {
		t.Errorf("text:\n%s", txt)
	}
	if _, overlap, _, _ := Touching(context.Background(), repo, repo, []string{"p.go"}, nil, false); !overlap {
		t.Error("x changes p.go too: want an overlap")
	}
	ws, _, left, _ = Touching(context.Background(), repo, repo, nil, nil, true)
	merged := map[string]bool{}
	for _, w := range ws {
		merged[w.Branch] = w.Merged
	}
	if len(ws) != 2 || !merged["fleet/p"] || merged["fleet/x"] || left != 0 {
		t.Errorf("--all: %+v, left %d", ws, left)
	}
}

