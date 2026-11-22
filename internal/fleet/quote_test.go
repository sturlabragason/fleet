package fleet

import (
	"strings"
	"testing"
	"time"
)

func TestQuote(t *testing.T) {
	at := time.Date(2026, 11, 22, 14, 5, 0, 0, time.Local)
	long := strings.Repeat("line\n", 12)
	tests := []struct {
		name string
		q    Quotable
		want string
	}{
		{"short", Quotable{Ref: "#3", T: at, Text: "Fix the date?\n\nOr leave it."},
			"[re #3, 14:05]\n> Fix the date?\n>\n> Or leave it.\n\n"},
		{"no time", Quotable{Ref: "#1", Text: "  one  \n"}, "[re #1]\n> one\n\n"},
		{"cut at 8", Quotable{Ref: "todo b5ea11fd", Text: long},
			"[re todo b5ea11fd]\n" + strings.Repeat("> line\n", QuoteLines) + "> …\n\n"},
		{"blank runs fold", Quotable{Ref: "#2", Text: "a\n\n\n\nb"}, "[re #2]\n> a\n>\n> b\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()
			if got := tt.q.Quote(); got != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
