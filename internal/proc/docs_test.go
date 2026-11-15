package proc

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDocs(t *testing.T) {
	d := t.TempDir()
	old := root
	root = d
	t.Cleanup(func() { root = old })
	fd := filepath.Join(d, "42", "fd")
	if err := os.MkdirAll(fd, 0o755); err != nil {
		t.Fatal(err)
	}
	argv := "soffice.bin\x00--impress\x00/w/review.pptx\x00"
	if err := os.WriteFile(filepath.Join(d, "42", "cmdline"), []byte(argv), 0o644); err != nil {
		t.Fatal(err)
	}
	for n, target := range map[string]string{
		"3": "/w/review.pptx", // also in argv: listed once
		"4": "/w/Budget.XLSX",
		"5": "/w/.~lock.review.pptx#",
		"6": "socket:[123]",
		"7": "/usr/lib/libreoffice/program/libmergedlo.so",
	} {
		if err := os.Symlink(target, filepath.Join(fd, n)); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"/w/Budget.XLSX", "/w/review.pptx"}
	if got := Docs(42); !slices.Equal(got, want) {
		t.Errorf("Docs = %q, want %q", got, want)
	}
	if got := Docs(43); got != nil {
		t.Errorf("Docs of a missing pid = %q", got)
	}
}

func TestOffice(t *testing.T) {
	for comm, want := range map[string]bool{"soffice.bin": true, "oosplash": true, "soffice": true, "node": false, "office-helper": false} {
		if Office(comm) != want {
			t.Errorf("Office(%q) = %v, want %v", comm, !want, want)
		}
	}
}
