package kagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sturlabragason/fleet/internal/ghapp"
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
		mintErr  error
		writeErr error
		wantErr  bool
		after    time.Duration
		writes   int
	}{
		{"refreshes ten minutes before expiry", nil, nil, false, 51 * time.Minute, 2},
		{"not before", nil, nil, false, 49 * time.Minute, 1},
		{"first mint fails", errors.New("401"), nil, true, time.Hour, 0},
		{"first write fails", nil, errors.New("forbidden"), true, time.Hour, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				mint := func(context.Context) (ghapp.Token, error) {
					return ghapp.Token{Value: "ghs", Expires: time.Now().Add(time.Hour)}, tc.mintErr
				}
				writes := 0
				write := func(context.Context, string) error {
					if tc.writeErr == nil {
						writes++
					}
					return tc.writeErr
				}
				err := KeepToken(ctx, mint, write)
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
