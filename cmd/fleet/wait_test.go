package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunWait(t *testing.T) {
	f := reportsHome(t)
	var out bytes.Buffer
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.Mail().Report("a", "done", "merged")
	}()
	if err := runWait(context.Background(), &out, f, "", "", []string{"a"}, 5*time.Second); err != nil || !strings.Contains(out.String(), "- a done: merged") {
		t.Fatalf("wait: %v %q", err, out.String())
	}
	out.Reset()
	var code exitCode
	if err := runWait(context.Background(), &out, f, "", "", nil, time.Second); !errors.As(err, &code) || code != 2 || !strings.HasPrefix(out.String(), "timeout after 1s") {
		t.Fatalf("timeout: %v %q", err, out.String())
	}
	if err := runWait(context.Background(), &out, f, "", "", []string{"zz"}, time.Second); err == nil {
		t.Fatal("unknown agent accepted")
	}
}
