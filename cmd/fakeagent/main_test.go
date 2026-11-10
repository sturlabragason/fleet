package main

import (
	"bufio"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The built binary reaches the ACP mode and [crash] really exits 3.
func TestACPCrashExits3(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "fakeagent")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "a1", "--acp", "--state", t.TempDir())
	in, _ := cmd.StdinPipe()
	outR, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	io.WriteString(in, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`+"\n")
	io.WriteString(in, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/","mcpServers":[]}}`+"\n")
	sc := bufio.NewScanner(outR)
	var sid string
	for sc.Scan() {
		if _, rest, ok := strings.Cut(sc.Text(), `"sessionId":"`); ok {
			sid, _, _ = strings.Cut(rest, `"`)
			break
		}
	}
	if sid == "" {
		t.Fatal("no session id")
	}
	io.WriteString(in, `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"`+sid+`","prompt":[{"type":"text","text":"[crash]"}]}}`+"\n")
	for sc.Scan() {
	}
	var ee *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("exit = %v, want code 3", err)
	}
}
