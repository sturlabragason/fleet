package tools

import (
	"os"
	"strings"
	"testing"
)

// A tools.toml that doesn't parse costs its own profiles, not the built-ins,
// and the error names the file and line, also through Broken.
func TestBrokenUserFile(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(Path(home), []byte("[mine]\ncommand = \"x\"\n[bad]\nacp = [\"--a\",\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := LoadWith(home, nil)
	if err == nil || !strings.Contains(err.Error(), Path(home)+": line 4: unterminated array") {
		t.Fatalf("err = %v", err)
	}
	if ps["copilot"].Command != "copilot" {
		t.Errorf("built-in copilot missing: %+v", ps["copilot"])
	}
	if err := Broken(home); err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("Broken = %v", err)
	}
	if err := os.WriteFile(Path(home), []byte("[mine]\ncommand = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Broken(home); err != nil {
		t.Fatalf("Broken after fix = %v", err)
	}
}
