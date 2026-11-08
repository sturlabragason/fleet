// Package web is `fleet web`: a browser front end beside the TUI and MCP
// (DESIGN.md §5f, ADR-66). It reads the board the watcher keeps fresh,
// captures panes read-only, and types only through internal/fleet's guarded
// paths (SendBlocker, the key allowlist, Choose with expect).
package web

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/watch"
)

//go:embed index.html
var indexHTML string

var page = template.Must(template.New("index").Parse(indexHTML))

const cookieName = "fleet_web"

// Server serves the web UI for one fleet.
type Server struct {
	F     *fleet.Fleet
	Token string
	// Hosts are the host names a request's Host and a POST's Origin may
	// carry (DNS rebinding and CSRF guard).
	Hosts map[string]bool
	// Poll is how often /events re-reads the board.
	Poll time.Duration
}

// New builds a Server listening as listen, accepting the loopback names, the
// listen host and extra (e.g. the gateway's public name).
func New(f *fleet.Fleet, token, listen string, extra []string) *Server {
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if h, _, err := net.SplitHostPort(listen); err == nil && h != "" {
		if ip := net.ParseIP(h); ip == nil || !ip.IsUnspecified() {
			hosts[strings.ToLower(h)] = true
		}
	}
	for _, h := range extra {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts[h] = true
		}
	}
	return &Server{F: f, Token: token, Hosts: hosts, Poll: 500 * time.Millisecond}
}

// LoadToken returns $FLEET_WEB_TOKEN, else the token kept in home/web.json,
// creating one (mode 0600) on first use. It stays the same across restarts
// so a proxy can be configured with it once.
func LoadToken(home string) (string, error) {
	if t := os.Getenv("FLEET_WEB_TOKEN"); t != "" {
		if len(t) < 16 {
			return "", fmt.Errorf("FLEET_WEB_TOKEN is too short (16 characters at least)")
		}
		return t, nil
	}
	path := filepath.Join(home, "web.json")
	var saved struct {
		Token string `json:"token"`
	}
	if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &saved) == nil && len(saved.Token) >= 16 {
		return saved.Token, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	saved.Token = hex.EncodeToString(buf)
	data, _ := json.Marshal(saved)
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", err
	}
	return saved.Token, os.WriteFile(path, data, 0o600)
}

func hostname(hostport string) string {
	h := hostport
	if hh, _, err := net.SplitHostPort(hostport); err == nil {
		h = hh
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

func (s *Server) tokenOK(t string) bool {
	return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.Token)) == 1
}

func (s *Server) authed(r *http.Request) bool {
	if c, err := r.Cookie(cookieName); err == nil && s.tokenOK(c.Value) {
		return true
	}
	t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && s.tokenOK(t)
}

// ServeHTTP checks Host, Origin and the token, then routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.Hosts[hostname(r.Host)] {
		http.Error(w, "fleet web: host not allowed (add it with --allow-host)", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Host is already allowed; the page's own origin carries the same
		// host:port, so another service on localhost:other is refused.
		o, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || o.Host == "" || !strings.EqualFold(o.Host, r.Host) {
			http.Error(w, "fleet web: cross-origin request refused", http.StatusForbidden)
			return
		}
	}
	if r.URL.Path == "/" && r.URL.Query().Has("token") {
		if !s.tokenOK(r.URL.Query().Get("token")) {
			http.Error(w, "fleet web: wrong token", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.Token, Path: "/", HttpOnly: true,
			SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.authed(r) {
		http.Error(w, "fleet web: open the URL `fleet web` printed; it carries the token", http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		s.index(w)
	case r.URL.Path == "/events" && r.Method == http.MethodGet:
		s.events(w, r)
	case r.URL.Path == "/api/peek" && r.Method == http.MethodGet:
		s.peek(w, r)
	case r.URL.Path == "/api/send" && r.Method == http.MethodPost:
		s.act(w, r, s.send)
	case r.URL.Path == "/api/keys" && r.Method == http.MethodPost:
		s.act(w, r, s.keys)
	case r.URL.Path == "/api/choose" && r.Method == http.MethodPost:
		s.act(w, r, s.choose)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) index(w http.ResponseWriter) {
	p := theme.Active()
	css := map[string]string{
		"bg": p.Base, "mantle": p.Mantle, "crust": p.Crust, "surface": p.Surface0, "surface1": p.Surface1,
		"fg": p.Text, "sub": p.Subtext0, "dim": p.Overlay0, "select": p.Select,
		"working": p.Working, "waiting": p.Waiting, "error": p.Error, "stuck": p.Stuck, "idle": p.Idle, "gone": p.Gone,
	}
	var b strings.Builder
	for k, v := range css {
		b.WriteString("--" + k + ":" + v + ";")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
	if err := page.Execute(w, template.CSS(b.String())); err != nil {
		logx.For("web").Error("render", "err", err)
	}
}

// Tile is one agent as the page shows it.
type Tile struct {
	Name    string    `json:"name"`
	Status  string    `json:"status"`
	Since   time.Time `json:"since"`
	Line    string    `json:"line"`
	Profile string    `json:"profile"`
	Goal    string    `json:"goal,omitempty"`
	Admiral string    `json:"admiral,omitempty"`
	Report  string    `json:"report,omitempty"`
}

// View is what /events pushes: every agent, and whether the watcher that
// keeps the board fresh is down.
type View struct {
	Agents []Tile `json:"agents"`
	Stale  bool   `json:"stale"`
}

func (s *Server) view() View {
	v := View{Agents: []Tile{}}
	for _, a := range s.F.Store.Load().Sorted() {
		t := Tile{Name: a.Name, Status: a.Status, Since: a.StatusSince, Line: a.LastLine,
			Profile: a.Profile, Goal: a.Goal, Admiral: a.Admiral}
		if a.Report != nil {
			first, _, _ := strings.Cut(a.Report.Summary, "\n")
			t.Report = a.Report.State + ": " + first
		}
		v.Agents = append(v.Agents, t)
	}
	b, ok := watch.ReadBeat(s.F.Home)
	v.Stale = !ok || time.Since(b.OK) > watch.StaleAfter
	return v
}

// events pushes the board over SSE whenever it changes. It only reads the
// board: the watcher stays the one refresher (ADR-49).
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	tick := time.NewTicker(s.Poll)
	defer tick.Stop()
	var last []byte
	for n := 0; ; n++ {
		data, _ := json.Marshal(s.view())
		if string(data) != string(last) {
			fmt.Fprintf(w, "data: %s\n\n", data)
			last = data
			fl.Flush()
		} else if n%30 == 0 { // keep proxies from timing the stream out
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

// Peek is one agent's screen and the menu on it, if any.
type Peek struct {
	HTML   string       `json:"html"`
	Menu   *status.Menu `json:"menu,omitempty"`
	Prompt string       `json:"prompt,omitempty"`
}

func (s *Server) peek(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	raw, err := s.F.PeekColor(name, 200)
	if err != nil {
		jsonErr(w, http.StatusNotFound, err)
		return
	}
	p := Peek{HTML: ANSIToHTML(strings.TrimRight(raw, "\n "))}
	if m, ok, _ := s.F.Menu(name); ok {
		p.Menu = &m
	} else if pr, _ := s.F.Prompting(name); pr != "" {
		p.Prompt = pr
	}
	writeJSON(w, http.StatusOK, p)
}

// Action is the body of every POST.
type Action struct {
	Name   string   `json:"name"`
	Text   string   `json:"text,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	Option int      `json:"option,omitempty"`
	Expect string   `json:"expect,omitempty"`
}

func (s *Server) act(w http.ResponseWriter, r *http.Request, do func(Action) (string, error)) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		jsonErr(w, http.StatusUnsupportedMediaType, fmt.Errorf("JSON only"))
		return
	}
	var a Action
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&a); err != nil || a.Name == "" {
		jsonErr(w, http.StatusBadRequest, fmt.Errorf("bad request"))
		return
	}
	msg, err := do(a)
	logx.For("web").Info("action", "path", r.URL.Path, "agent", a.Name, "keys", a.Keys, "option", a.Option, "bytes", len(a.Text), "err", err)
	if err != nil {
		jsonErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": msg})
}

// send pastes text and presses Enter, unless the user is typing there.
func (s *Server) send(a Action) (string, error) {
	if strings.TrimSpace(a.Text) == "" {
		return "", fmt.Errorf("nothing to send")
	}
	if why := s.F.SendBlocker(a.Name, true); why != "" {
		return "", fmt.Errorf("not sent: %s", why)
	}
	return "sent", s.F.Send(a.Name, a.Text)
}

// keys presses allowlisted keys, under the same don't-type-over-the-user check.
func (s *Server) keys(a Action) (string, error) {
	if len(a.Keys) == 0 {
		return "", fmt.Errorf("no keys")
	}
	if err := fleet.CheckKeys(a.Keys); err != nil {
		return "", err
	}
	submit := false
	for _, k := range a.Keys {
		submit = submit || k == "Enter"
	}
	if why := s.F.SendBlocker(a.Name, submit); why != "" {
		return "", fmt.Errorf("not sent: %s", why)
	}
	return "pressed", s.F.SendKeys(a.Name, "", a.Keys)
}

// choose picks a menu option; expect is the question the user saw, so a
// prompt that changed since is refused.
func (s *Server) choose(a Action) (string, error) {
	if a.Expect == "" {
		return "", fmt.Errorf("expect is required: the prompt's question as shown")
	}
	if why := s.F.SendBlocker(a.Name, false); why != "" {
		return "", fmt.Errorf("not sent: %s", why)
	}
	c, err := s.F.Choose(a.Name, a.Option, a.Expect)
	return "chose " + c.Label, err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
