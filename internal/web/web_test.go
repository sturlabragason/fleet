package web

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
)

var update = flag.Bool("update", false, "rewrite golden files")

const tok = "0123456789abcdef0123456789abcdef"

// testServer is a fleet on a fleet-test-* socket with a "cat" agent and a
// "menu" agent, served over httptest.
func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	t.Setenv("FLEET_WEB_TOKEN", "")
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[cat]
command = "sh"
args = ["-c", "cat"]
patterns = "fake"
[menu]
command = "sh"
args = ["-c", "stty -echo; printf 'Do you want to?\n❯ 1. Yes\n  2. No\n'; cat -v"]
patterns = "fake"
`), 0o644)
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	for n, p := range map[string]string{"c1": "cat", "m1": "menu"} {
		if _, err := f.Spawn(n, p, home, ""); err != nil {
			t.Fatal(err)
		}
	}
	s := New(f, tok, "127.0.0.1:0", []string{"fleet.example.org"})
	s.Poll = 20 * time.Millisecond
	hs := httptest.NewServer(s)
	t.Cleanup(hs.Close)
	return s, hs
}

func req(t *testing.T, hs *httptest.Server, method, path, body string, hdr map[string]string) *http.Response {
	t.Helper()
	r, _ := http.NewRequest(method, hs.URL+path, strings.NewReader(body))
	for k, v := range hdr {
		if k == "Host" {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	c := *hs.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func waitScreen(t *testing.T, f *fleet.Fleet, name, want string) string {
	t.Helper()
	var out string
	for i := 0; i < 60; i++ {
		if out, _ = f.Peek(name, 0); strings.Contains(out, want) {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never showed %q: %q", name, want, out)
	return ""
}

func TestAuth(t *testing.T) {
	_, hs := testServer(t)
	bearer := map[string]string{"Authorization": "Bearer " + tok}
	// Caddy passes the browser's Host through, so Origin and Host agree.
	gw := map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json", "Origin": "https://fleet.example.org", "Host": "fleet.example.org"}
	post := func(origin string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json", "Origin": origin}
	}
	for _, c := range []struct {
		name, method, path, body string
		hdr                      map[string]string
		want                     int
	}{
		{"no token", "GET", "/", "", nil, 401},
		{"wrong token in URL", "GET", "/?token=nope", "", nil, 401},
		{"wrong bearer", "GET", "/", "", map[string]string{"Authorization": "Bearer nope"}, 401},
		{"wrong cookie", "GET", "/", "", map[string]string{"Cookie": cookieName + "=nope"}, 401},
		{"bearer", "GET", "/", "", bearer, 200},
		{"cookie", "GET", "/", "", map[string]string{"Cookie": cookieName + "=" + tok}, 200},
		{"rebinding host", "GET", "/", "", map[string]string{"Authorization": "Bearer " + tok, "Host": "evil.example:80"}, 403},
		{"gateway host", "GET", "/", "", map[string]string{"Authorization": "Bearer " + tok, "Host": "fleet.example.org"}, 200},
		{"post without origin", "POST", "/api/keys", `{"name":"c1","keys":["y"]}`, map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"}, 403},
		{"post cross origin", "POST", "/api/keys", `{"name":"c1","keys":["y"]}`, post("https://evil.example"), 403},
		{"post from another localhost port", "POST", "/api/keys", `{"name":"c1","keys":["y"]}`, post("http://localhost:1"), 403},
		{"post not json", "POST", "/api/keys", `{"name":"c1","keys":["y"]}`, map[string]string{"Authorization": "Bearer " + tok, "Origin": hs.URL}, 415},
		{"key not allowed", "POST", "/api/keys", `{"name":"c1","keys":["C-c"]}`, post(hs.URL), 409},
		{"own origin", "POST", "/api/keys", `{"name":"c1","keys":["y"]}`, post(hs.URL), 200},
		{"gateway origin", "POST", "/api/keys", `{"name":"c1","keys":["y"]}`, gw, 200},
	} {
		if got := req(t, hs, c.method, c.path, c.body, c.hdr).StatusCode; got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
	// The token URL trades the token for an HttpOnly cookie.
	resp := req(t, hs, "GET", "/?token="+tok, "", nil)
	ck := resp.Cookies()
	if resp.StatusCode != http.StatusSeeOther || len(ck) != 1 || ck[0].Value != tok || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteStrictMode {
		t.Errorf("token exchange: %d %+v", resp.StatusCode, ck)
	}
}

func TestSendLandsAndRespectsTheUser(t *testing.T) {
	s, hs := testServer(t)
	hdr := map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json", "Origin": hs.URL}
	if r := req(t, hs, "POST", "/api/send", `{"name":"c1","text":"hello from web"}`, hdr); r.StatusCode != 200 {
		t.Fatalf("send: %d", r.StatusCode)
	}
	waitScreen(t, s.F, "c1", "hello from web")

	// The user just typed in c1's pane: the web must not type over them.
	s.F.Store.Update(func(b *board.Board) error { b.Agents["c1"].UserAt = time.Now(); return nil })
	for _, path := range []string{"/api/send", "/api/keys", "/api/choose"} {
		r := req(t, hs, "POST", path, `{"name":"c1","text":"x","keys":["Enter"],"option":1,"expect":"q"}`, hdr)
		var body strings.Builder
		sc := bufio.NewScanner(r.Body)
		for sc.Scan() {
			body.WriteString(sc.Text())
		}
		if r.StatusCode != 409 || !strings.Contains(body.String(), "user typed") {
			t.Errorf("%s while the user types: %d %s", path, r.StatusCode, body.String())
		}
	}
}

func TestChooseRefusesAStalePrompt(t *testing.T) {
	s, hs := testServer(t)
	hdr := map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json", "Origin": hs.URL}
	waitScreen(t, s.F, "m1", "2. No")
	r := req(t, hs, "GET", "/api/peek?name=m1", "", hdr)
	var body strings.Builder
	sc := bufio.NewScanner(r.Body)
	for sc.Scan() {
		body.WriteString(sc.Text())
	}
	if !strings.Contains(body.String(), `"Question":"Do you want to?"`) {
		t.Fatalf("peek has no menu: %s", body.String())
	}
	if r := req(t, hs, "POST", "/api/choose", `{"name":"m1","option":2,"expect":"Delete everything?"}`, hdr); r.StatusCode != 409 {
		t.Errorf("stale choose: %d", r.StatusCode)
	}
	if r := req(t, hs, "POST", "/api/choose", `{"name":"m1","option":2,"expect":"Do you want to?"}`, hdr); r.StatusCode != 200 {
		t.Errorf("choose: %d", r.StatusCode)
	}
	waitScreen(t, s.F, "m1", "^[")
}

func TestEventsPushTheBoard(t *testing.T) {
	_, hs := testServer(t)
	r, _ := http.NewRequest("GET", hs.URL+"/events", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	resp, err := hs.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	if !strings.HasPrefix(line, "data: ") || !strings.Contains(line, `"name":"c1"`) || !strings.Contains(line, `"stale":true`) {
		t.Errorf("first event: %q", line)
	}
}

func TestLoadTokenIsStableAndPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_WEB_TOKEN", "")
	a, err := LoadToken(home)
	if err != nil || len(a) != 64 {
		t.Fatalf("token %q %v", a, err)
	}
	if b, _ := LoadToken(home); b != a {
		t.Error("token changed across loads")
	}
	if st, _ := os.Stat(filepath.Join(home, "web.json")); st.Mode().Perm() != 0o600 {
		t.Errorf("web.json mode %v", st.Mode())
	}
	t.Setenv("FLEET_WEB_TOKEN", "short")
	if _, err := LoadToken(home); err == nil {
		t.Error("a short FLEET_WEB_TOKEN must be refused")
	}
}

func TestANSIToHTMLEdges(t *testing.T) {
	for in, want := range map[string]string{
		"a\x1b":                    "a",
		"a\x1b]8;;http://x":        "a",
		"\x1b[31m<x>\x1b[0m":       `<span style="color:#cd0000;">&lt;x&gt;</span>`,
		"\x1b[38;5;999mx":          "x",
		"\x1b[38;2;1;2mx":          "x",
		"\x1b[2Jcleared\x1b[1;1H!": "cleared!",
	} {
		if got := ANSIToHTML(in); got != want {
			t.Errorf("ANSIToHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestANSIToHTMLGolden(t *testing.T) {
	in, err := os.ReadFile("testdata/capture.ansi")
	if err != nil {
		t.Fatal(err)
	}
	got := ANSIToHTML(string(in))
	if *update {
		os.WriteFile("testdata/capture.html", []byte(got), 0o644)
	}
	want, _ := os.ReadFile("testdata/capture.html")
	if got != string(want) {
		t.Errorf("ANSIToHTML differs from testdata/capture.html (go test -update rewrites it):\n%s", got)
	}
}
