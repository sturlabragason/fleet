// Package ghapp mints GitHub App installation tokens (DESIGN §7d, ADR-145 g):
// a JWT signed with the App's private key buys a token that lasts one hour.
package ghapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// App is one GitHub App installation.
type App struct {
	ID           string // App ID or client ID (the JWT's iss)
	Installation string // installation ID
	Key          *rsa.PrivateKey
	API          string // "" = https://api.github.com
	Client       *http.Client
	Now          func() time.Time
}

// Token is an installation access token.
type Token struct {
	Value   string
	Expires time.Time
}

// Load reads dir's app-id, installation-id and private-key.pem. The key must
// be readable by its owner only.
func Load(dir string) (*App, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", fmt.Errorf("github app: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	id, err := read("app-id")
	if err != nil {
		return nil, err
	}
	inst, err := read("installation-id")
	if err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "private-key.pem")
	fi, err := os.Stat(keyPath)
	if err != nil {
		return nil, fmt.Errorf("github app: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("github app: %s is readable by others; chmod 600 it", keyPath)
	}
	pemKey, err := read("private-key.pem")
	if err != nil {
		return nil, err
	}
	key, err := ParseKey([]byte(pemKey))
	if err != nil {
		return nil, err
	}
	return &App{ID: id, Installation: inst, Key: key}, nil
}

// ParseKey reads a PEM RSA key, PKCS#1 (what GitHub hands out) or PKCS#8.
func ParseKey(b []byte) (*rsa.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("github app: private key is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app: parse private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app: private key is not RSA")
	}
	return rk, nil
}

// JWT is the App's own credential: RS256, backdated a minute for clock
// skew, valid nine (GitHub allows ten).
func (a *App) JWT() (string, error) {
	now := a.now()
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.ID,
	})
	if err != nil {
		return "", fmt.Errorf("github app jwt: %w", err)
	}
	signed := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("github app jwt: %w", err)
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

// Mint buys an installation token with the installation's permissions and
// repositories.
func (a *App) Mint(ctx context.Context) (Token, error) {
	jwt, err := a.JWT()
	if err != nil {
		return Token{}, err
	}
	api := a.API
	if api == "" {
		api = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", strings.TrimRight(api, "/"), a.Installation)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
	if err != nil {
		return Token{}, fmt.Errorf("github app token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	c := a.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("github app token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, fmt.Errorf("github app token: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		var e struct{ Message string }
		_ = json.Unmarshal(body, &e) // a non-JSON body leaves the status as the whole error
		return Token{}, fmt.Errorf("github app token: %s %s", resp.Status, e.Message)
	}
	var t struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return Token{}, fmt.Errorf("github app token: %w", err)
	}
	if t.Token == "" {
		return Token{}, errors.New("github app token: empty token in reply")
	}
	return Token{Value: t.Token, Expires: t.ExpiresAt}, nil
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}
