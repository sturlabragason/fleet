//go:build extexample

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
)

func TestExampleExtPage(t *testing.T) {
	s := New(&fleet.Fleet{Home: "/h"}, tok, "127.0.0.1:0", nil)
	get := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := get("/x/example/hello", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := get("/x/example/hello", tok); w.Code != http.StatusOK || w.Body.String() != "example page for /h" {
		t.Fatalf("page: %d %q", w.Code, w.Body.String())
	}
	for _, p := range []string{"/x/example/nope", "/x/other/hello"} {
		if w := get(p, tok); w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "http://localhost/x/example/hello", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Origin", "http://localhost")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST: %d", w.Code)
	}
}
