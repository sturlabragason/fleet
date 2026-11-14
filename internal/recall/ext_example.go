//go:build extexample

package recall

import "context"

// The public example extension (DESIGN §15 "Extensions"): it fills every
// seam of this package so public CI compiles and tests them. Its day
// section only shows for a home under exampleHome.
var exampleHome string

func init() {
	Register(Extension{
		Name: "example",
		Day: func(ctx context.Context, o DayOpts) string {
			if exampleHome == "" || o.Home != exampleHome {
				return ""
			}
			return "## Example\nexample day section"
		},
	})
}
