package kagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestGitHubSecretManifest(t *testing.T) {
	m, err := GitHubSecret{Namespace: "kagent", Name: "github-push"}.manifest("ghs_abc")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Kind     string
		Metadata struct{ Name, Namespace string }
		Data     map[string]string
	}
	if err := json.Unmarshal(m, &s); err != nil {
		t.Fatal(err)
	}
	// data is base64 of the value; the value is the Basic credential, base64.
	outer, err := base64.StdEncoding.DecodeString(s.Data["authorization"])
	if err != nil {
		t.Fatal(err)
	}
	basic, err := base64.StdEncoding.DecodeString(string(outer))
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "Secret" || s.Metadata.Name != "github-push" || s.Metadata.Namespace != "kagent" || string(basic) != "x-access-token:ghs_abc" {
		t.Fatalf("manifest = %s (credential %q)", m, basic)
	}
}

func TestKeepToken(t *testing.T) {
	tests := []struct {
		name     string
		getErr   error
		writeErr error
		wantErr  bool
		after    time.Duration
		writes   int
	}{
		{"refreshes every 30 minutes", nil, nil, false, 61 * time.Minute, 3},
		{"not before", nil, nil, false, 29 * time.Minute, 1},
		{"first get fails", errors.New("no token"), nil, true, time.Hour, 0},
		{"first write fails", nil, errors.New("forbidden"), true, time.Hour, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				get := func(context.Context) (string, error) { return "ghs", tc.getErr }
				writes := 0
				write := func(context.Context, string) error {
					if tc.writeErr == nil {
						writes++
					}
					return tc.writeErr
				}
				err := KeepToken(ctx, get, write)
				if (err != nil) != tc.wantErr {
					t.Fatalf("err = %v", err)
				}
				time.Sleep(tc.after)
				synctest.Wait()
				if writes != tc.writes {
					t.Fatalf("writes = %d, want %d", writes, tc.writes)
				}
				cancel()
				synctest.Wait()
			})
		})
	}
}

func TestTokenSources(t *testing.T) {
	dir := t.TempDir()
	file := func(name, body string, mode os.FileMode) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name    string
		get     func(context.Context) (string, error)
		want    string
		wantErr string
	}{
		{"command", TokenCmd("printf 'ghs_abc\\n'"), "ghs_abc", ""},
		{"command fails", TokenCmd("echo boom >&2; exit 3"), "", "boom"},
		{"command prints two words", TokenCmd("echo hunter2 extra"), "", "one token on one line"},
		{"file", TokenFile(file("ok", "ghp_abc\n", 0o600)), "ghp_abc", ""},
		{"file readable by group", TokenFile(file("open", "ghp_abc", 0o640)), "", "chmod 600"},
		{"no file", TokenFile(filepath.Join(dir, "missing")), "", "no GitHub token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.get(t.Context())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || strings.Contains(err.Error(), "hunter2") {
					t.Fatalf("err = %v, want %q and no token", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}
