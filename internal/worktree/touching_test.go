package worktree

import (
	"context"
	"os"
	"path/filepath"
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
	// A merged worktree with a new, unstaged file still shows: fresh work.
	fresh := Path(repo, "fresh")
	run(t, repo, "worktree", "add", "-q", "-b", "fleet/fresh", fresh)
	os.WriteFile(filepath.Join(fresh, "new.go"), []byte("n\n"), 0o644)
	os.WriteFile(filepath.Join(fresh, "result.md"), []byte("r\n"), 0o644)
	ws, _, left, _ = Touching(context.Background(), repo, repo, []string{"new.go"}, nil, false)
	if len(ws) != 1 || ws[0].Branch != "fleet/fresh" || left != 1 {
		t.Errorf("fresh work: %+v, left %d", ws, left)
	}
	os.Remove(filepath.Join(fresh, "new.go"))
	ws, _, left, _ = Touching(context.Background(), repo, repo, nil, nil, false)
	if len(ws) != 1 || left != 2 {
		t.Errorf("fresh with only result.md: %+v, left %d; want it left out", ws, left)
	}
	run(t, repo, "worktree", "remove", "--force", fresh)
	ws, _, left, _ = Touching(context.Background(), repo, repo, nil, nil, true)
	merged := map[string]bool{}
	for _, w := range ws {
		merged[w.Branch] = w.Merged
	}
	if len(ws) != 2 || !merged["fleet/p"] || merged["fleet/x"] || left != 0 {
		t.Errorf("--all: %+v, left %d", ws, left)
	}
}
