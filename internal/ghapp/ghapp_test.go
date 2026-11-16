package ghapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// checkJWT verifies an RS256 JWT against pub and returns its claims.
func checkJWT(t *testing.T, tok string, pub *rsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("jwt signature: %v", err)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMint(t *testing.T) {
	key := testKey(t)
	now := time.Unix(1_800_000_000, 0)
	tests := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr string
	}{
		{"created", http.StatusCreated, `{"token":"ghs_abc","expires_at":"2027-01-15T09:00:00Z"}`, "ghs_abc", ""},
		{"bad credentials", http.StatusUnauthorized, `{"message":"A JSON web token could not be decoded"}`, "", "401 Unauthorized A JSON web token"},
		{"empty token", http.StatusCreated, `{"expires_at":"2027-01-15T09:00:00Z"}`, "", "empty token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/app/installations/42/access_tokens" {
					t.Errorf("request %s %s", r.Method, r.URL.Path)
				}
				c := checkJWT(t, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), &key.PublicKey)
				if c["iss"] != "123" || c["iat"] != float64(now.Unix()-60) || c["exp"] != float64(now.Unix()+540) {
					t.Errorf("claims = %v", c)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			a := &App{ID: "123", Installation: "42", Key: key, API: srv.URL, Now: func() time.Time { return now }}
			tok, err := a.Mint(t.Context())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tok.Value != tc.want || !tok.Expires.Equal(time.Date(2027, 1, 15, 9, 0, 0, 0, time.UTC)) {
				t.Fatalf("token = %+v", tok)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	key := testKey(t)
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	p8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: p8})
	tests := []struct {
		name    string
		pem     []byte
		mode    os.FileMode
		wantErr string
	}{
		{"pkcs1", pkcs1, 0o600, ""},
		{"pkcs8", pkcs8, 0o600, ""},
		{"readable by group", pkcs1, 0o640, "chmod 600"},
		{"not pem", []byte("nope"), 0o600, "not PEM"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name string, b []byte, mode os.FileMode) {
				t.Helper()
				p := filepath.Join(dir, name)
				if err := os.WriteFile(p, b, mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(p, mode); err != nil { // umask may have narrowed it
					t.Fatal(err)
				}
			}
			write("app-id", []byte("123\n"), 0o644)
			write("installation-id", []byte(" 42\n"), 0o644)
			write("private-key.pem", tc.pem, tc.mode)
			a, err := Load(dir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if a.ID != "123" || a.Installation != "42" || !a.Key.Equal(key) {
				t.Fatalf("app = %+v", a)
			}
		})
	}
}
