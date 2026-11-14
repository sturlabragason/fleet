//go:build extexample

package web

import (
	"fmt"
	"net/http"

	"github.com/sturlabragason/fleet/internal/fleet"
)

// The public example extension (DESIGN §15 "Extensions"): it fills every
// seam of this package so public CI compiles and tests them.
func init() {
	Register(Extension{
		Name: "example",
		Pages: map[string]Page{"hello": func(f *fleet.Fleet, w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "example page for %s", f.Home)
		}},
	})
}
