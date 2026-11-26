package prefs

import (
	"fmt"

	"github.com/sturlabragason/fleet/internal/browser"
	"github.com/sturlabragason/fleet/internal/conf"
)

// BrowserNeverOpen is [browser] never_open: agents whose name, route or
// profile carries one of these words never open a page (ADR-200). Read on
// its own, by the short-lived browser guard. A missing file or key is the
// default list; never_open = [] turns the list off.
func BrowserNeverOpen() ([]string, error) {
	path := Path()
	d, err := conf.ParseFile(path)
	if err != nil {
		return browser.DefaultNeverOpen, fmt.Errorf("%s: %w", path, err)
	}
	sec := d["browser"]
	if _, ok := sec["never_open"]; !ok {
		return browser.DefaultNeverOpen, nil
	}
	return conf.List(sec, "never_open"), nil
}
