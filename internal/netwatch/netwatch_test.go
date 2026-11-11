package netwatch

import (
	"net"
	"testing"
	"time"
)

func TestNetError(t *testing.T) {
	for _, s := range []string{
		// copilot --acp session/new, no network (captured 2026-11-11)
		"session (new): error sending request for url (https://api.individual.githubcopilot.com/meta): client error (Connect): tcp connect error: Network is unreachable (os error 101)",
		"Error: Failed to load models",
		"API Error: Connection error.",
		"request to https://api.anthropic.com failed, reason: getaddrinfo EAI_AGAIN api.anthropic.com",
		"dial tcp: lookup github.com: no such host",
	} {
		if !NetError(s) {
			t.Errorf("not a network error: %q", s)
		}
	}
	for _, s := range []string{"", "agent exited: exit status 3", "prompt: rate limited", "Authentication required"} {
		if NetError(s) {
			t.Errorf("network error: %q", s)
		}
	}
}

func TestDialAny(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	dead.Close()
	if !dialAny([]string{deadAddr, ln.Addr().String()}, time.Second) {
		t.Error("one listener answering is online")
	}
	if dialAny([]string{deadAddr}, time.Second) {
		t.Error("nothing answering is offline")
	}
}

func TestTargetsProxy(t *testing.T) {
	t.Setenv("FLEET_NET_PROBE", "")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTPS_PROXY", "http://proxy.local:3128")
	if got := targets(); len(got) != 1 || got[0] != "proxy.local:3128" {
		t.Errorf("targets %v", got)
	}
	t.Setenv("FLEET_NET_PROBE", "a:1,b:2")
	if got := targets(); len(got) != 2 || got[1] != "b:2" {
		t.Errorf("targets %v", got)
	}
}
