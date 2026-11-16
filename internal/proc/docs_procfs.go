//go:build !darwin

package proc

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Docs lists the documents pid has open or was started with: its argv and
// its open file descriptors, sorted, without duplicates.
func Docs(pid int) []string {
	dir := filepath.Join(root, strconv.Itoa(pid))
	var out []string
	add := func(p string) {
		if docExts[strings.ToLower(filepath.Ext(p))] && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		for a := range bytes.SplitSeq(b, []byte{0}) {
			add(string(a))
		}
	}
	if fds, err := os.ReadDir(filepath.Join(dir, "fd")); err == nil {
		for _, fd := range fds {
			if p, err := os.Readlink(filepath.Join(dir, "fd", fd.Name())); err == nil {
				add(p)
			}
		}
	}
	slices.Sort(out)
	return out
}
