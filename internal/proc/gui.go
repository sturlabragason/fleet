package proc

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// guiLibs are the libraries a process maps to draw on the user's desktop.
var guiLibs = [][]byte{[]byte("/libX11.so"), []byte("/libxcb.so"), []byte("/libwayland-client.so")}

// GUI reports whether pid is a desktop app, such as a LibreOffice window,
// a browser or a viewer an agent opened for the user (xdg-open). Killing
// it would close the user's windows and documents, so closing or parking
// the agent leaves it running. LibreOffice is known by name (its launcher
// may not have drawn yet); anything else by a display library it has
// mapped. A --headless process is not one.
func GUI(pid int) bool {
	dir := filepath.Join(root, strconv.Itoa(pid))
	if b, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil && bytes.Contains(b, []byte("--headless")) {
		return false
	}
	if b, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil && Office(strings.TrimSpace(string(b))) {
		return true
	}
	b, err := os.ReadFile(filepath.Join(dir, "maps"))
	if err != nil {
		return false
	}
	for _, lib := range guiLibs {
		if bytes.Contains(b, lib) {
			return true
		}
	}
	return false
}
