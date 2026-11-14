package web

import (
	"net/http"
	"strings"

	"github.com/sturlabragason/fleet/internal/fleet"
)

// Extension is what a feature compiled in with a build tag adds to `fleet
// web` (DESIGN §15 "Extensions", ADR-127). It calls Register from its own
// init(); a default build registers nothing.
type Extension struct {
	Name string
	// Pages are GET pages served at /x/<Name>/<path>, keyed by path ("" is
	// /x/<Name>/), behind the same host, origin and token checks as the
	// rest of the server.
	Pages map[string]Page
}

// Page serves one extension page for a fleet.
type Page func(f *fleet.Fleet, w http.ResponseWriter, r *http.Request)

var exts = map[string]Extension{}

// Register adds an extension. Call it from init(). A name registered twice,
// or one with a slash, panics.
func Register(e Extension) {
	if e.Name == "" || strings.Contains(e.Name, "/") {
		panic("web.Register: bad extension name " + e.Name)
	}
	if _, ok := exts[e.Name]; ok {
		panic("web.Register: extension " + e.Name + " registered twice")
	}
	exts[e.Name] = e
}

// extPage serves /x/<name>/<path>; an unknown name or path is a 404.
func (s *Server) extPage(w http.ResponseWriter, r *http.Request) {
	name, path, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/x/"), "/")
	if !ok {
		http.Redirect(w, r, "/x/"+name+"/", http.StatusSeeOther)
		return
	}
	h := exts[name].Pages[path]
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h(s.F, w, r)
}
