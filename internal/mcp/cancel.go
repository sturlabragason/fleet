package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Cancellation of slow calls. The serve loop is serial, so while a slow
// call runs a reader goroutine reads the client's next lines on the loop's
// behalf: a notifications/cancelled for the running call's id cancels its
// context and the call gets no response (the spec: SHOULD NOT respond,
// MUST NOT on 2026-07-28 stdio). Every line it reads, the cancel included,
// is handed to the serve loop in order.

var (
	// errCancelled is the cause when the client cancelled the running call.
	errCancelled = errors.New("cancelled by the client")
	// errInterrupted is the cause when another line ended a fleet_wait
	// (ADR-72): the client did not cancel it, so it is answered.
	errInterrupted = errors.New("interrupted by the next message")
)

// aheadMax is how many lines the reader buffers while a call runs; past it
// the reader waits for the loop and sees no further cancel until then.
// shortcut: that also holds back a dialog's answer (elicit.go) until its
// timeout; upgrade with the concurrent serve loop if a client is seen
// sending 64 lines while a dialog is open.
const aheadMax = 64

type lineRead struct {
	raw []byte
	err error
}

// watch is the slow call the reader watches lines for.
type watch struct {
	id      json.RawMessage
	anyLine bool // fleet_wait: any line ends it
	ctx     context.Context
	cancel  context.CancelCauseFunc
}

// callCtx is the context of a slow tools/call. It ends when the client
// sends notifications/cancelled for this call's id, and then the call gets
// no response; with anyLine (fleet_wait) any other line ends it too, and
// the call is answered. A following request does not end a call without
// anyLine: the client may send calls in parallel. stop must be called when
// the call returns, on the serving goroutine.
func (s *Server) callCtx(anyLine bool) (ctx context.Context, stop func()) {
	s.wmu.Lock()
	if w := s.watching; w != nil && idKey(w.id) == idKey(s.callID) {
		// Nested in the same call (a CLI tool's confirmation dialog): its
		// watch already reads; the outer stop ends it.
		s.wmu.Unlock()
		return w.ctx, func() {}
	}
	s.wmu.Unlock()
	ctx, cancel := context.WithCancelCause(context.Background())
	if s.in == nil {
		return ctx, func() { cancel(nil) }
	}
	w := &watch{id: s.callID, anyLine: anyLine, ctx: ctx, cancel: cancel}
	s.wmu.Lock()
	s.watching = w
	// Lines the reader checked before this call started, still queued.
	switch {
	case s.cancelled[idKey(w.id)]:
		delete(s.cancelled, idKey(w.id))
		cancel(errCancelled)
	case anyLine && s.queued > 0:
		cancel(errInterrupted)
	}
	start := !s.reading
	s.reading = true
	s.wmu.Unlock()
	if start {
		if s.ahead == nil {
			s.ahead = make(chan lineRead, aheadMax)
			s.cancelled = map[string]bool{}
		}
		prev, done := s.aheadDone, make(chan struct{})
		s.aheadDone = done
		go s.readAhead(prev, done)
	}
	return ctx, func() {
		s.wmu.Lock()
		if s.watching == w {
			s.watching = nil
		}
		s.wmu.Unlock()
		if errors.Is(context.Cause(ctx), errCancelled) {
			s.drop = true
		}
		cancel(nil)
	}
}

// readAhead reads lines into s.ahead while a call is watched. It decides to
// stop before it pushes its last line, so a reader that stopped never
// touches s.in again; done closes after that push. prev is the previous
// reader's done: its last line goes first.
func (s *Server) readAhead(prev, done chan struct{}) {
	if prev != nil {
		<-prev
	}
	defer close(done)
	for {
		raw, err := readLine(s.in)
		if err == nil && s.deliver(raw) {
			continue
		}
		s.wmu.Lock()
		w := s.watching
		id, isCancel := cancelID(raw)
		switch {
		case w != nil && isCancel && idKey(id) == idKey(w.id):
			w.cancel(errCancelled)
		case isCancel: // for a request still queued, or one already served
			s.cancelled[idKey(id)] = true
		}
		if w != nil && (err != nil || w.anyLine) {
			w.cancel(errInterrupted) // no-op when cancelled above
		}
		// Decided under wmu before the push, so callCtx starts a new reader
		// once this one stops.
		last := err != nil || w == nil || w.ctx.Err() != nil
		if last {
			s.reading = false
		}
		s.queued++
		s.wmu.Unlock()
		s.ahead <- lineRead{raw, err}
		if last {
			return
		}
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

// forgetCancel drops a cancel id once the serve loop reached that cancel
// line: any request it named was served before it.
func (s *Server) forgetCancel(id json.RawMessage) {
	s.wmu.Lock()
	delete(s.cancelled, idKey(id))
	s.wmu.Unlock()
}

// next is the next input line: the oldest one read ahead, else a fresh read.
func (s *Server) next() ([]byte, error) {
	for s.aheadDone != nil {
		select {
		case r := <-s.ahead:
			if s.took() { // its last push is due (there is room now): wait it out
				<-s.aheadDone
				s.aheadDone = nil
			}
			return r.raw, r.err
		case <-s.aheadDone:
			s.aheadDone = nil
		}
	}
	select {
	case r := <-s.ahead:
		s.took()
		return r.raw, r.err
	default:
	}
	return readLine(s.in)
}

// took counts one line taken from s.ahead and reports whether the reader
// has stopped.
func (s *Server) took() (stopped bool) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.queued--
	return !s.reading
}

// aheadIdle is true when no reader holds s.in and no line waits in
// s.ahead, so a reload may hand the input over.
func (s *Server) aheadIdle() bool {
	return s.aheadDone == nil && len(s.ahead) == 0
}

// Responses to the server's own requests (elicitation/create). The reader
// routes a response to the waiter of its id instead of the serve loop;
// await registers before the request is sent, unawait drops it.

// await registers a waiter for the response to id.
func (s *Server) await(id json.RawMessage) <-chan []byte {
	ch := make(chan []byte, 1)
	s.wmu.Lock()
	if s.replies == nil {
		s.replies = map[string]chan []byte{}
	}
	s.replies[idKey(id)] = ch
	s.wmu.Unlock()
	return ch
}

func (s *Server) unawait(id json.RawMessage) {
	s.wmu.Lock()
	delete(s.replies, idKey(id))
	s.wmu.Unlock()
}

// deliver takes every response line: to its waiter, or to the log when
// nobody waits (late, after a timeout), so it never reaches the loop or
// ends a fleet_wait. False when the line is not a response.
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
