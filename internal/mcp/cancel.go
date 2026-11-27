package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/sturlabragason/fleet/internal/logx"
)

// The concurrent serve loop (ADR-214). Serve is the reader: it reads every
// line as it arrives and never waits for a call. A response goes to the
// waiter of its id (deliver); a notifications/cancelled cancels the call of
// its id; any other line ends a fleet_wait in flight (ADR-72) and is
// dispatched. Each request gets its own goroutine and context, and the
// goroutines take turns in arrival order (the lane), so replies, the board
// and the result suffixes behave as with the old serial loop. All writes go
// through write, one at a time.

var (
	// errCancelled is the cause when the client cancelled the call: it gets
	// no response (the spec: SHOULD NOT respond, MUST NOT on 2026-07-28 stdio).
	errCancelled = errors.New("cancelled by the client")
	// errInterrupted is the cause when another line ended a fleet_wait
	// (ADR-72) or the input closed: the client did not cancel it, so it is
	// answered.
	errInterrupted = errors.New("interrupted by the next message")
)

// call is one request in flight: queued in the lane or running.
type call struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	id     json.RawMessage
	wait   bool // fleet_wait: the client's next line ends it (ADR-72)
	// byClient is set by a notifications/cancelled for this id. Kept apart
	// from the context's cause: a wait interrupted first keeps that cause,
	// and a later cancel must still win (no reply).
	byClient atomic.Bool
	// peer is the client a modern request describes in its _meta (modern.go);
	// nil for a legacy request.
	peer *clientInfo
	// mrtr is a modern tools/call's MRTR fields (mrtr.go); nil otherwise.
	mrtr *mrtrIn
	// progress sends notifications/progress for the call's progressToken;
	// nil without one. Set and used on the call's goroutine.
	progress func(n int, msg string)
}

// cancelled is true when the client cancelled the call.
func (c *call) cancelled() bool {
	return c != nil && c.byClient.Load()
}

// callCtx is the running call's context: it ends when the client cancels
// the call, the input closes, or (fleet_wait) the client's next line
// arrives. Outside Serve (tests calling handlers directly) it never ends.
func (s *Server) callCtx() context.Context {
	if s.cur == nil {
		return context.Background()
	}
	return s.cur.ctx
}

// progress is the running call's progress sender, nil when the client sent
// no progressToken.
func (s *Server) progress() func(n int, msg string) {
	if s.cur == nil {
		return nil
	}
	return s.cur.progress
}

// write sends one protocol message; every writer goes through it. The
// first error is kept for Serve, which then stops.
func (s *Server) write(v any) error {
	s.omu.Lock()
	defer s.omu.Unlock()
	if s.out == nil {
		return errors.New("no output: not serving")
	}
	err := s.out.Encode(v)
	if err != nil && s.werr == nil {
		s.werr = err
	}
	return err
}

// writeErr is the first failed write's error.
func (s *Server) writeErr() error {
	s.omu.Lock()
	defer s.omu.Unlock()
	return s.werr
}

// arrived applies a line to the calls in flight before it is dispatched: a
// cancel cancels the call of its id, and every line ends a fleet_wait.
// Cancel first: a cancel of the wait itself must win, so it gets no reply.
func (s *Server) arrived(line []byte) {
	id, isCancel := cancelID(line)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if c := s.calls[idKey(id)]; isCancel && c != nil {
		c.byClient.Store(true)
		c.cancel(errCancelled)
	}
	if isCancel && s.calls[idKey(id)] == nil {
		s.dropSub(id) // closed with no further message, as the spec requires
	}
	for _, c := range s.calls {
		if c.wait {
			c.cancel(errInterrupted)
		}
	}
}

// dispatch starts a line's goroutine at the end of the lane. A parse error
// is answered here; a cancel was applied by arrived and needs nothing more.
func (s *Server) dispatch(line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		logx.For("mcp").Warn("bad json", "err", err)
		_ = s.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}) // a failed write ends Serve via writeErr
		return
	}
	if req.Method == "notifications/cancelled" {
		return
	}
	peer := modernOf(req.Params)
	if peer != nil {
		s.modern = true
		if req.Method == "subscriptions/listen" && len(req.ID) > 0 && modernCheck(req.Method, peer.Protocol) == nil {
			s.listen(req.ID, req.Params) // never ends, so it stays out of the lane
			return
		}
	}
	var p struct {
		Name string `json:"name"`
	}
	ctx, cancel := context.WithCancelCause(s.base)
	c := &call{ctx: ctx, cancel: cancel, id: req.ID, peer: peer,
		wait: req.Method == "tools/call" && json.Unmarshal(req.Params, &p) == nil && p.Name == "fleet_wait"}
	s.wmu.Lock()
	s.flight++
	if len(req.ID) > 0 {
		if s.calls == nil {
			s.calls = map[string]*call{}
		}
		s.calls[idKey(req.ID)] = c
	}
	s.wmu.Unlock()
	prev, done := s.tail, make(chan struct{})
	s.tail = done
	go s.run(c, req, prev, done)
}

// run serves one request when its turn in the lane comes (prev closed) and
// closes done after its response is out. A call cancelled while it was
// queued never runs.
func (s *Server) run(c *call, req request, prev, done chan struct{}) {
	defer close(done)
	if prev != nil {
		<-prev
	}
	defer s.end(c)
	log := logx.For("mcp")
	if c.cancelled() {
		log.Info("cancelled by the client before it ran", "id", string(req.ID))
		return
	}
	if err := s.writeErr(); err != nil { // no reply could go out: don't act
		log.Info("not run: output closed", "id", string(req.ID), "err", err)
		return
	}
	s.cur = c
	res, rerr := s.handle(req)
	s.cur = nil
	if len(req.ID) == 0 { // notification
		return
	}
	if c.cancelled() {
		log.Info("cancelled by the client", "id", string(req.ID))
		return
	}
	resp := response{JSONRPC: "2.0", ID: req.ID, Result: res, Error: rerr}
	switch {
	case rerr == nil && c.peer != nil:
		resp.Result = modernResult(req.Method, res)
	case rerr == nil && res == nil:
		resp.Result = struct{}{}
	}
	if err := s.write(resp); err != nil {
		log.Info("response not sent", "id", string(req.ID), "err", err)
	}
}

// end takes a finished call out of flight.
func (s *Server) end(c *call) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.flight--
	if s.calls[idKey(c.id)] == c {
		delete(s.calls, idKey(c.id))
	}
	c.cancel(nil)
	s.idle.Broadcast()
}

// drain waits until no call is in flight, so a reload can hand the input
// over. It gives up (false) while a call waits for an answer to its dialog:
// only the reader can bring that answer in, so the reload waits for a
// later line.
func (s *Server) drain() bool {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	for s.flight > 0 && len(s.replies) == 0 {
		s.idle.Wait()
	}
	return s.flight == 0
}

// finish ends serving: every call in flight is interrupted (the client is
// gone) and answered, and finish returns once all are done.
func (s *Server) finish() {
	s.stop(errInterrupted) // every call's context derives from base, duplicate ids too
	s.wmu.Lock()
	defer s.wmu.Unlock()
	for s.flight > 0 {
		s.idle.Wait()
	}
}

// cancelID is the requestId of a notifications/cancelled line.
func cancelID(raw []byte) (json.RawMessage, bool) {
	var m struct {
		Method string `json:"method"`
		Params struct {
			RequestID json.RawMessage `json:"requestId"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Method != "notifications/cancelled" || len(m.Params.RequestID) == 0 {
		return nil, false
	}
	return m.Params.RequestID, true
}

// idKey is a request id as a map key ("" for none, which matches no key in use).
func idKey(id json.RawMessage) string { return string(bytes.TrimSpace(id)) }

// Responses to the server's own requests (elicitation/create). The reader
// routes a response to the waiter of its id; await registers before the
// request is sent, unawait drops it.

// await registers a waiter for the response to id. A waiter also stops a
// reload's drain (drain), so it is broadcast.
func (s *Server) await(id json.RawMessage) <-chan []byte {
	ch := make(chan []byte, 1)
	s.wmu.Lock()
	if s.replies == nil {
		s.replies = map[string]chan []byte{}
	}
	s.replies[idKey(id)] = ch
	s.idle.Broadcast()
	s.wmu.Unlock()
	return ch
}

func (s *Server) unawait(id json.RawMessage) {
	s.wmu.Lock()
	delete(s.replies, idKey(id))
	s.wmu.Unlock()
}

// deliver takes every response line: to its waiter, or to the log when
// nobody waits (late, after a timeout, or an id fleet never sent), so it
// is never dispatched and never ends a fleet_wait. False when the line is
// not a response.
func (s *Server) deliver(raw []byte) bool {
	id, ok := responseID(raw)
	if !ok {
		return false
	}
	s.wmu.Lock()
	ch, ok := s.replies[idKey(id)]
	delete(s.replies, idKey(id))
	s.wmu.Unlock()
	if ok {
		ch <- raw
	} else {
		logx.For("mcp").Info("response nobody waits for", "id", string(id))
	}
	return true
}

// responseID is the id of a JSON-RPC response line (no method, a result or
// an error).
func responseID(raw []byte) (json.RawMessage, bool) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Method != "" || len(m.ID) == 0 || (m.Result == nil && m.Error == nil) {
		return nil, false
	}
	return m.ID, true
}
