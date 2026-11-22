package main

import (
	"strings"
	"testing"
)

const designFixture = `# Title
## 4. The CLI
cli text
### Sub
sub text
## 7b. Spend per agent
spend text
## 14. Decisions
**ADR-71: count compactions** (§7b)
adr 71 body
**ADR-72: fleet_wait blocks** (§4)
adr 72 body
## 15. Next
`

func TestDesignSection(t *testing.T) {
	for _, c := range []struct {
		query, want, not string
	}{
		{"ADR-71", "**ADR-71: count compactions** (§7b)\nadr 71 body\n", "adr 72"},
		{"72", "adr 72 body\n", "## 15"},
		{"§7b", "## 7b. Spend per agent\nspend text\n", "Decisions"},
		{"4", "", ""}, // a bare number is an ADR, and there is no ADR-4
		{"§", "", ""},
		{"§4", "cli text\n### Sub\nsub text\n", "7b"},
		{"wait blocks", "adr 72 body", "adr 71"},
		{"spend", "spend text", "Also matching"},
	} {
		t.Run(c.query, func(t *testing.T) {
			got, err := designSection(designFixture, c.query)
			if c.want == "" {
				if err == nil {
					t.Fatalf("want an error, got %q", got)
				}
				return
			}
			if err != nil || !strings.Contains(got, c.want) || c.not != "" && strings.Contains(got, c.not) {
				t.Fatalf("designSection(%q) = %q, %v; want %q without %q", c.query, got, err, c.want, c.not)
			}
		})
	}
}
