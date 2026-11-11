// Package netwatch tells whether the machine can reach the model APIs, and
// whether a line of agent output is a network failure (DESIGN §7e, ADR-110).
package netwatch

import (
	"context"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Targets are dialled by Probe: the hosts the agents' tools talk to. Any
// one answering is enough.
var Targets = []string{"api.githubcopilot.com:443", "api.anthropic.com:443", "github.com:443"}

// Timeout bounds each dial.
var Timeout = 4 * time.Second

// Probe reports whether any target accepts a TCP connection. With an
// HTTPS_PROXY set the proxy is dialled instead: it is what the tools reach.
// FLEET_NET_PROBE replaces the targets (host:port, comma separated).
func Probe() bool {
	return dialAny(targets(), Timeout)
}

// Enabled is false when FLEET_NET_PROBE is "off": no probing, and fleet
// treats the network as always up.
func Enabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("FLEET_NET_PROBE")), "off")
}

func targets() []string {
	if v := strings.TrimSpace(os.Getenv("FLEET_NET_PROBE")); v != "" {
		return strings.Split(v, ",")
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy"} {
		if p := proxyAddr(os.Getenv(k)); p != "" {
			return []string{p}
		}
	}
	return Targets
}

func proxyAddr(v string) string {
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Port() != "" {
		return u.Host
	}
	if u.Scheme == "https" {
		return u.Host + ":443"
	}
	return u.Host + ":80"
}

func dialAny(addrs []string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ok := make(chan bool, len(addrs))
	var wg sync.WaitGroup
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var d net.Dialer
			c, err := d.DialContext(ctx, "tcp", a)
			if err == nil {
				c.Close()
				ok <- true
			}
		}()
	}
	go func() { wg.Wait(); close(ok) }()
	_, any := <-ok
	return any
}

// netErr matches what copilot, claude, agy and Go/Node network stacks print
// when the network is gone. Captured from `copilot --acp` and `copilot -p`
// in an empty network namespace (2026-11-11).
var netErr = regexp.MustCompile(`(?i)network is unreachable|tcp connect error|error sending request|` +
	`could not connect to the copilot api|failed to load models|connection error|unable to connect|` +
	`getaddrinfo|enotfound|eai_again|econnrefused|econnreset|etimedout|enetunreach|ehostunreach|` +
	`no such host|fetch failed|socket hang up|temporary failure in name resolution|` +
	`dial tcp|i/o timeout|network error|connection refused|connection reset`)

// NetError reports whether s reads as a network failure.
func NetError(s string) bool { return s != "" && netErr.MatchString(s) }
