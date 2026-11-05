package summary

import (
	"context"
	"strings"
	"testing"
	"time"
)

// These never call a model: they drive the CLI backend with plain shell
// tools, so the test suite stays offline and free.

func TestCLIPassesThePromptAndTheModel(t *testing.T) {
	c := &CLI{Command: "printf", Args: []string{"%s|%s", "{prompt}", "{model}"}}
	out, err := c.Summarize(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello|"+DefaultModel {
		t.Fatalf("out = %q", out)
	}
	if c.Name() != DefaultModel {
		t.Fatalf("default model = %q", c.Name())
	}
}

func TestCLIWithoutAPromptPlaceholderUsesStdin(t *testing.T) {
	c := &CLI{Command: "cat", Args: []string{}, Model: "claude-haiku-5.5"}
	out, err := c.Summarize(context.Background(), "from stdin")
	if err != nil {
		t.Fatal(err)
	}
	if out != "from stdin" {
		t.Fatalf("out = %q", out)
	}
}

func TestCLIFailures(t *testing.T) {
	missing := &CLI{Command: "fleet-no-such-cli-xyz"}
	if _, err := missing.Summarize(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v", err)
	}
	slow := &CLI{Command: "sleep", Args: []string{"30"}, Timeout: 100 * time.Millisecond}
	start := time.Now()
	if _, err := slow.Summarize(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the timeout did not stop the call")
	}
	bad := &CLI{Command: "sh", Args: []string{"-c", "echo nope >&2; exit 3"}}
	if _, err := bad.Summarize(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestDefaultArgsAreNonInteractive(t *testing.T) {
	args := strings.Join(DefaultArgs(), " ")
	for _, want := range []string{"-p", "{prompt}", "--model {model}", "--silent",
		"--no-custom-instructions", "--available-tools none"} {
		if !strings.Contains(args, want) {
			t.Fatalf("default args missing %q: %s", want, args)
		}
	}
}

// A CLI that leaves a child holding its stdout must not outlive its timeout:
// without a process-group kill and a WaitDelay, Run blocks forever.
func TestCLITimeoutSurvivesABackgroundChild(t *testing.T) {
	c := &CLI{Command: "sh", Args: []string{"-c", "sleep 60 & exit 0"}, Timeout: 300 * time.Millisecond}
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := c.Summarize(context.Background(), "x"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error")
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Fatalf("took %s", d)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Summarize never returned: a grandchild is holding the pipe")
	}
}
