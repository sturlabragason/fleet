package tools

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// command is the argv[0] the profile starts: its command, or with
// native_binary the Copilot native binary its Node launcher would run. When
// that binary can't be found the launcher is kept and the reason logged.
func (p Profile) command(v Vars) string {
	c := v.expand(p.Command)
	if !p.NativeBinary {
		return c
	}
	n, err := NativeBinary(c)
	if err != nil {
		logx.For("tools").Warn("native_binary: keeping the launcher", "profile", p.Name, "command", c, "err", err)
		return c
	}
	return n
}

// NativeBinary resolves the native Copilot CLI binary behind cmd, skipping
// the Node launcher (npm-loader.js) that `npm install -g @github/copilot`
// puts on PATH. The launcher runs the first of
// @github/copilot-<platform>-<arch> that Node module resolution finds from
// its own dir, so this looks in the same node_modules dirs. A cmd that is
// already a native binary (not a script) is returned as is. cmd without a
// slash is looked up in ~/.local/bin first, like the spawn PATH (EnvList).
func NativeBinary(cmd string) (string, error) {
	path, err := lookCommand(cmd)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !isScript(real) {
		return real, nil
	}
	return findNative(filepath.Dir(real), runtime.GOOS, runtime.GOARCH)
}

func lookCommand(cmd string) (string, error) {
	if strings.Contains(cmd, "/") {
		return cmd, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", cmd)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return exec.LookPath(cmd)
}

func isScript(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 2)
	n, _ := f.Read(b)
	return bytes.Equal(b[:n], []byte("#!"))
}

// findNative walks up from dir, as Node module resolution does, for
// node_modules/@github/copilot-<platform>-<arch>/copilot.
func findNative(dir, goos, goarch string) (string, error) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	plats := map[string][]string{"linux": {"linux", "linuxmusl"}, "darwin": {"darwin"}}[goos]
	if arch == "" || plats == nil {
		return "", fmt.Errorf("no native Copilot package for %s/%s", goos, goarch)
	}
	for d := dir; ; d = filepath.Dir(d) {
		for _, pl := range plats {
			p := filepath.Join(d, "node_modules", "@github", "copilot-"+pl+"-"+arch, "copilot")
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
				return p, nil
			}
		}
		if filepath.Dir(d) == d {
			return "", errors.New("no @github/copilot-" + plats[0] + "-" + arch + " package found from " + dir)
		}
	}
}
