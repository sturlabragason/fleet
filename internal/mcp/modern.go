package mcp

// The modern era (MCP 2026-07-28, ADR-215). fleet is dual-era: initialize
// selects legacy semantics for the process (server.go), and a request whose
// _meta carries io.modelcontextprotocol/protocolVersion is served on its own,
// statelessly, by that version. The two can mix on one stdio connection.

import (
	"cmp"
	"encoding/json"
	"os"
	"slices"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
)

// ModernVersion is the per-request protocol revision fleet serves.
const ModernVersion = "2026-07-28"

var modernVersions = []string{ModernVersion}

const (
	metaVersion = "io.modelcontextprotocol/protocolVersion"
	metaServer  = "io.modelcontextprotocol/serverInfo"
	metaSub     = "io.modelcontextprotocol/subscriptionId"
	// envListen carries the open subscriptions/listen requests across a hot
	// reload (DESIGN §6.9): the new process sends list_changed on them.
	envListen = "FLEET_MCP_LISTEN"
	// errUnsupportedVersion is UnsupportedProtocolVersionError's code.
	errUnsupportedVersion = -32022
)

// removedModern are the methods 2026-07-28 does not have; a modern request
// for one is method not found, a legacy one is still served. A modern
// initialize must not take over the process's legacy client.
var removedModern = []string{"initialize", "ping", "logging/setLevel"}

// supportedVersions lists every version fleet serves, modern first; a
// legacy one is reached through initialize.
func supportedVersions() []string { return append(slices.Clone(modernVersions), legacyVersions...) }

// modern reports whether c came from a modern request's _meta.
func (c clientInfo) modern() bool { return slices.Contains(modernVersions, c.Protocol) }

// modernOf is the client a modern request describes in its _meta (version,
// clientInfo, clientCapabilities), or nil for a legacy request. The version
// key alone makes a request modern: a version that is not a string keeps
// its JSON text and gets -32022, and a malformed clientInfo is dropped
// (it is for display only), so odd input never falls back to legacy.
func modernOf(params json.RawMessage) *clientInfo {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	raw, ok := p.Meta[metaVersion]
	if !ok {
		return nil
	}
	c := &clientInfo{Capabilities: p.Meta["io.modelcontextprotocol/clientCapabilities"]}
	if json.Unmarshal(raw, &c.Protocol) != nil {
		c.Protocol = string(raw)
	}
	var info clientInfo
	if json.Unmarshal(p.Meta["io.modelcontextprotocol/clientInfo"], &info) == nil {
		c.Name, c.Version = info.Name, info.Version
	}
	return c
}

// peer is who sent the running request: a modern request's own _meta
// (never inferred from earlier requests), else the initialize client.
func (s *Server) peer() clientInfo {
	if s.cur != nil && s.cur.peer != nil {
		return *s.cur.peer
	}
	return s.client
}

// modernCheck refuses a modern request fleet can't serve: an unsupported
// version (-32022, with the versions fleet supports) or a removed method.
func modernCheck(method, version string) *rpcError {
	if !slices.Contains(modernVersions, version) {
		return &rpcError{Code: errUnsupportedVersion, Message: "Unsupported protocol version",
			Data: map[string]any{"supported": supportedVersions(), "requested": version}}
	}
	if slices.Contains(removedModern, method) {
		return &rpcError{Code: -32601, Message: "method not found: " + method}
	}
	return nil
}

// serverInfo is fleet's Implementation.
func serverInfo() map[string]any {
	return map[string]any{"name": "fleet", "title": "Fleet", "version": cmp.Or(fleet.Version, "dev")}
}

func serverCaps() map[string]any {
	return map[string]any{"tools": map[string]any{"listChanged": true}}
}

// discover is the DiscoverResult. Its instructions are initialize's, with
// the same served-hash bookkeeping (ADR-102). ttlMs 0: the instructions and
// tools change with a reload, and a refetch over stdio costs nothing.
func (s *Server) discover() map[string]any {
	return cacheable(map[string]any{
		"resultType":        "complete",
		"supportedVersions": supportedVersions(),
		"capabilities":      serverCaps(),
		"instructions":      s.serveInstructions(),
		"_meta":             map[string]any{metaServer: serverInfo()},
	})
}

// cacheable adds CacheableResult's fields. private: the tools and
// instructions depend on the client and the admiral.
func cacheable(m map[string]any) map[string]any {
	m["ttlMs"] = 0
	m["cacheScope"] = "private"
	return m
}

// modernResult adds what every modern result carries (resultType, serverInfo
// in _meta) and, for tools/list, the cache fields. Legacy results never pass
// through here, so they stay as they were.
func modernResult(method string, res any) any {
	m, ok := res.(map[string]any)
	if !ok || m == nil {
		m = map[string]any{} // nil and struct{}{}: an empty result
	}
	if _, ok := m["resultType"]; !ok {
		m["resultType"] = "complete"
	}
	if _, ok := m["_meta"]; !ok {
		m["_meta"] = map[string]any{metaServer: serverInfo()}
	}
	if method == "tools/list" {
		cacheable(m)
	}
	return m
}

// listenSub is one open subscriptions/listen request. fleet has tools only,
// so toolsListChanged is the one filter it honours.
type listenSub struct {
	ID    json.RawMessage `json:"id"`
	Tools bool            `json:"tools,omitempty"`
}

// listenState is what a reload hands over in envListen.
type listenState struct {
	// Modern: the client sent a modern request, so without initialize it
	// gets list_changed only on a listen stream that asked for it.
	Modern bool        `json:"modern,omitempty"`
	Subs   []listenSub `json:"subs,omitempty"`
}

// listen opens a subscription: it acknowledges the filter fleet honours and
// sends no response, which goes out only when fleet ends the stream; an
// abrupt close (EOF) carries none. It runs on the reader, outside the lane,
// since the request never ends. A notifications/cancelled for its id closes
// it (arrived).
func (s *Server) listen(id, params json.RawMessage) {
	var p struct {
		Notifications *struct {
			Tools bool `json:"toolsListChanged"`
		} `json:"notifications"`
	}
	if json.Unmarshal(params, &p) != nil || p.Notifications == nil {
		_ = s.write(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "invalid params: notifications filter required"}}) // a failed write ends Serve via writeErr
		return
	}
	sub := listenSub{ID: id, Tools: p.Notifications.Tools}
	s.dropSub(id)
	s.subs = append(s.subs, sub)
	ack := map[string]any{}
	if sub.Tools {
		ack["toolsListChanged"] = true
	}
	_ = s.write(subNote("notifications/subscriptions/acknowledged", id, map[string]any{"notifications": ack})) // a failed write ends Serve via writeErr
}

// dropSub closes the subscription of id, if one is open.
func (s *Server) dropSub(id json.RawMessage) {
	s.subs = slices.DeleteFunc(s.subs, func(x listenSub) bool { return idKey(x.ID) == idKey(id) })
}

// subNote is a notification on the listen stream of id.
func subNote(method string, id json.RawMessage, params map[string]any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = map[string]any{metaSub: id}
	return map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
}

// announceReload tells the client its tools changed after a reload: the
// legacy notification unless the client is modern only (no initialize),
// and on every listen stream that asked for tool changes.
func (s *Server) announceReload() error {
	if !s.modern || s.client.Protocol != "" {
		if err := s.write(listChanged); err != nil {
			return err
		}
	}
	for _, sub := range s.subs {
		if sub.Tools {
			if err := s.write(subNote("notifications/tools/list_changed", sub.ID, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}

// listenEnv is the reload env entry for the modern state; "" when none.
func (s *Server) listenEnv() string {
	if !s.modern && len(s.subs) == 0 {
		return ""
	}
	b, err := json.Marshal(listenState{Modern: s.modern, Subs: s.subs})
	if err != nil {
		return ""
	}
	return envListen + "=" + string(b)
}

// takeListen adopts the modern state a reloaded process inherited and
// clears it so children don't inherit it.
func (s *Server) takeListen() {
	if v := os.Getenv(envListen); v != "" && s.Reloaded {
		var st listenState
		if err := json.Unmarshal([]byte(v), &st); err != nil {
			logx.For("mcp").Warn("reloaded listen state", "err", err)
		}
		s.modern, s.subs = st.Modern, st.Subs
	}
	os.Unsetenv(envListen)
}
