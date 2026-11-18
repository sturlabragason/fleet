package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An admiral with only fleet tools reads all of a 24 KB saved reply through
// fleet_brief file=… pages, each under pageBytes, and can search it; files
// that are not saved replies under the temp dir are refused.
func TestBriefReadsSavedReply(t *testing.T) {
	s, _, _ := memServer(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	var want strings.Builder
	for i := 0; want.Len() < 24<<10; i++ {
		fmt.Fprintf(&want, "line-%04d %s\n", i, strings.Repeat("é", 40))
	}
	spill := filepath.Join(tmp, "1763-copilot-tool-output-abc.txt")
	if err := os.WriteFile(spill, []byte(want.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	var got strings.Builder
	for off, pages := 0, 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging does not end")
		}
		out, isErr := callText(t, s, "fleet_brief", map[string]any{"file": spill, "offset": off})
		head, body, _ := strings.Cut(out, "\n")
		if isErr || len(body) > pageBytes {
			t.Fatalf("page at %d: error=%v, %d bytes", off, isErr, len(body))
		}
		got.WriteString(body)
		if strings.HasSuffix(head, "; end") {
			break
		}
		if _, err := fmt.Sscanf(head[strings.Index(head, "offset=")+len("offset="):], "%d", &off); err != nil {
			t.Fatalf("no next offset in %q", head)
		}
	}
	if got.String() != want.String() {
		t.Error("pages do not add up to the file")
	}
	if out, _ := callText(t, s, "fleet_brief", map[string]any{"file": spill, "query": "LINE-0123"}); !strings.Contains(out, "1 lines match") || !strings.Contains(out, "124 (offset=") {
		t.Errorf("query:\n%s", out)
	}

	outside := filepath.Join(t.TempDir(), "x-copilot-tool-output-y.txt")
	_ = os.WriteFile(outside, []byte("secret"), 0o600)
	link := filepath.Join(tmp, "2-copilot-tool-output-link.txt")
	_ = os.Symlink(filepath.Join(s.F.Home, "pinned.log"), link)
	plain := filepath.Join(tmp, "notes.txt")
	_ = os.WriteFile(plain, []byte("secret"), 0o600)
	for _, f := range []string{outside, link, plain, "relative-copilot-tool-output.txt", filepath.Join(tmp, "..", filepath.Base(outside))} {
		if out, isErr := callText(t, s, "fleet_brief", map[string]any{"file": f}); !isErr || strings.Contains(out, "secret") {
			t.Errorf("%s was not refused: %s", f, out)
		}
	}
}
