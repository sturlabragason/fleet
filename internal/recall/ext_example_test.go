//go:build extexample

package recall

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The section comes after "Sessions outside fleet", also without cass.
func TestExampleExtDay(t *testing.T) {
	t.Setenv("FLEET_CASS", "off")
	exampleHome = fleetHome(t)
	defer func() { exampleHome = "" }()
	out := Day(context.Background(), DayOpts{Home: exampleHome, Since: time.Now().Add(-time.Hour)})
	_, after, ok := strings.Cut(out, "## Sessions outside fleet")
	if !ok || !strings.HasSuffix(after, CassHint+"\n\n## Example\nexample day section\n") {
		t.Fatalf("digest:\n%s", out)
	}
}
