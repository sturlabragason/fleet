package acphost

import (
	"slices"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/acp"
)

func TestSteer(t *testing.T) {
	cases := []struct {
		name     string
		steerOut string // the agent advertises steering and answers this
		how      string // Request.Steer
		busy     bool   // a turn runs when the steer comes
		want     string
		calls    []string // after "initialize", "new", the first prompt
	}{
		{"idle agent gets a prompt", "", "", false, DeliverySent, []string{"prompt fix"}},
		{"native steer joins the turn", acp.SteerInjected, SteerCancel, true, DeliverySteered, []string{"steer fix"}},
		{"native steer after the turn ended waits for its return", acp.SteerPromptRequired, "", true, DeliveryQueued, []string{"steer fix"}},
		{"second prompt joins the turn", "", SteerPrompt, true, DeliverySteered, []string{"prompt fix"}},
		{"otherwise the turn is cancelled", "", "", true, DeliveryInterrupted, []string{"cancel", "prompt " + InterruptNote + "fix"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := newStubConn(acp.AgentCapabilities{})
			conn.steerOut = tc.steerOut
			cfg := Config{}
			if tc.busy {
				cfg.Prompt = "go"
			}
			r := startRigOn(t, cfg, conn)
			if tc.busy {
				r.waitStatus("working")
			}
			before := len(conn.Calls())
			resp, err := r.call(Request{Op: OpSteer, Text: "fix", Steer: tc.how})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Delivery != tc.want {
				t.Fatalf("delivery = %q, want %q", resp.Delivery, tc.want)
			}
			r.waitFor(func() bool { return len(conn.Calls()) >= before+len(tc.calls) }, "the steer's calls")
			if got := conn.Calls()[before:]; !slices.Equal(got[:len(tc.calls)], tc.calls) {
				t.Fatalf("calls = %q, want %q", got, tc.calls)
			}
			if tc.want == DeliveryQueued { // sent once the turn returns
				conn.turns <- func(handler) acp.StopReason { return acp.StopEndTurn }
				r.waitFor(func() bool { return slices.Contains(conn.Calls(), "prompt fix") }, "the held steer sent")
			}
			if tc.want == DeliverySteered && strings.Contains(strings.Join(conn.Calls(), "|"), "cancel") {
				t.Fatalf("a steer cancelled the turn: %q", conn.Calls())
			}
		})
	}
}
