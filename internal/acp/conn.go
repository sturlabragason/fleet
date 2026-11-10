package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sturlabragason/fleet/internal/logx"
	"io"
	"strconv"
	"sync"
)

// ErrClosed is returned by calls on a connection that was closed or whose
// reader ended.
var ErrClosed = errors.New("acp: connection closed")

// requestFunc serves one agent-to-client request. A *RPCError return is sent
// as is; any other error becomes CodeInternal.
type requestFunc func(ctx context.Context, method string, params json.RawMessage) (any, error)

// notifyFunc handles one agent-to-client notification.
type notifyFunc func(method string, params json.RawMessage)

// conn is newline-delimited JSON-RPC 2.0 over a reader and a writer.
//
// One reader goroutine parses frames and queues them; one dispatch goroutine
// takes them in arrival order: it calls onNotify, completes pending calls and
// starts each agent request on its own goroutine. Responses go through the
// same queue, so a call returns only after every notification that arrived
// before its response was handled. onNotify must therefore not block on a
// call of its own connection.
type conn struct {
	r  io.Reader
	w  io.Writer
	wc io.Closer // w, when it can be closed

	onNotify  notifyFunc
	onRequest requestFunc

	wmu sync.Mutex // one frame per Write

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan result
	closed  bool
	ended   bool // finish ran: no new pending calls
	forced  bool // the reader was closed on purpose (Start's exit grace)
	readErr error
	exitErr error
	queue   []Message
	readEnd bool
	wake    chan struct{} // buffered 1: the dispatcher has work

	ctx    context.Context // cancelled on Close or when the conn ends
	cancel context.CancelFunc

	extra chan struct{} // closed when an external condition (child exit) holds; nil if none
	done  chan struct{}
}

type result struct {
	msg Message
	err error
}

func newConn(r io.Reader, w io.Writer, onNotify notifyFunc, onRequest requestFunc, extra chan struct{}) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{
		r: r, w: w,
		onNotify: onNotify, onRequest: onRequest,
		pending: map[string]chan result{},
		wake:    make(chan struct{}, 1),
		ctx:     ctx, cancel: cancel,
		extra: extra,
		done:  make(chan struct{}),
	}
	if wc, ok := w.(io.Closer); ok {
		c.wc = wc
	}
	go c.read()
	go c.dispatch()
	return c
}

// read splits the stream into lines of any length and queues the frames.
func (c *conn) read() {
	br := bufio.NewReaderSize(c.r, 64*1024)
	var err error
	for {
		var line []byte
		line, err = br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			c.parse(line)
		}
		if err != nil {
			break
		}
	}
	c.mu.Lock()
	if !errors.Is(err, io.EOF) && !c.forced {
		c.readErr = err
	}
	c.readEnd = true
	c.mu.Unlock()
	c.signal()
}

func (c *conn) parse(line []byte) {
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		logx.For("acp").Warn("acp: malformed frame skipped", "err", err, "len", len(line))
		return
	}
	if isNullID(m.ID) {
		m.ID = nil
	}
	if m.ID == nil && m.Method == "" {
		logx.For("acp").Warn("acp: frame with neither id nor method skipped")
		return
	}
	c.mu.Lock()
	c.queue = append(c.queue, m)
	c.mu.Unlock()
	c.signal()
}

func (c *conn) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *conn) dispatch() {
	for {
		c.mu.Lock()
		q := c.queue
		c.queue = nil
		end := c.readEnd
		c.mu.Unlock()
		for _, m := range q {
			c.handle(m)
		}
		if len(q) == 0 && end {
			break
		}
		if len(q) == 0 {
			<-c.wake
		}
	}
	if c.extra != nil {
		<-c.extra
	}
	c.finish()
}

// finish fails the pending calls, cancels request handlers and closes done.
func (c *conn) finish() {
	c.mu.Lock()
	c.ended = true
	pend := c.pending
	c.pending = map[string]chan result{}
	err := c.endErrLocked()
	c.mu.Unlock()
	for _, ch := range pend {
		ch <- result{err: err}
	}
	c.cancel()
	close(c.done)
}

func (c *conn) endErrLocked() error {
	if c.exitErr != nil {
		return fmt.Errorf("%w: %w", ErrClosed, c.exitErr)
	}
	if c.readErr != nil {
		return fmt.Errorf("%w: %w", ErrClosed, c.readErr)
	}
	return ErrClosed
}

func (c *conn) handle(m Message) {
	switch {
	case m.Method != "" && m.ID != nil:
		go c.serve(m)
	case m.Method != "":
		if c.onNotify != nil {
			c.onNotify(m.Method, m.Params)
		}
	default:
		key := idKey(m.ID)
		c.mu.Lock()
		ch, ok := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if !ok {
			logx.For("acp").Warn("acp: response to unknown id skipped", "id", string(m.ID))
			return
		}
		ch <- result{msg: m}
	}
}

// serve answers one agent request, echoing its id unchanged.
func (c *conn) serve(m Message) {
	reply := Message{JSONRPC: "2.0", ID: m.ID}
	var res any
	var err error
	if c.onRequest == nil {
		err = &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + m.Method}
	} else {
		res, err = c.onRequest(c.ctx, m.Method, m.Params)
	}
	if err != nil {
		var re *RPCError
		if !errors.As(err, &re) {
			re = &RPCError{Code: CodeInternal, Message: err.Error()}
		}
		reply.Error = re
	} else {
		b, merr := json.Marshal(res)
		if merr != nil {
			reply.Error = &RPCError{Code: CodeInternal, Message: merr.Error()}
		} else {
			reply.Result = b
		}
	}
	if werr := c.write(reply); werr != nil {
		logx.For("acp").Warn("acp: reply not sent", "method", m.Method, "err", werr)
	}
}

func (c *conn) write(m Message) error {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(b)
	return err
}

// call sends a request and waits for its response, decoding the result into
// out (unless nil).
func (c *conn) call(ctx context.Context, method string, params, out any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan result, 1)
	c.mu.Lock()
	if c.closed || c.ended {
		err := c.endErrLocked()
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := json.RawMessage(strconv.FormatInt(c.nextID, 10))
	key := idKey(id)
	c.pending[key] = ch
	c.mu.Unlock()

	if err := c.write(Message{ID: id, Method: method, Params: p}); err != nil {
		c.forget(key)
		return fmt.Errorf("acp: %s: %w", method, err)
	}
	select {
	case <-ctx.Done():
		c.forget(key)
		return ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if r.msg.Error != nil {
			return r.msg.Error
		}
		if out != nil && len(r.msg.Result) > 0 {
			if err := json.Unmarshal(r.msg.Result, out); err != nil {
				return fmt.Errorf("acp: %s result: %w", method, err)
			}
		}
		return nil
	}
}

func (c *conn) forget(key string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

func (c *conn) notify(method string, params any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	return c.write(Message{Method: method, Params: p})
}

// close closes the writer, fails pending calls and cancels request handlers.
// The reader keeps running until its stream ends.
func (c *conn) close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	pend := c.pending
	c.pending = map[string]chan result{}
	c.mu.Unlock()
	for _, ch := range pend {
		ch <- result{err: ErrClosed}
	}
	c.cancel()
	if c.wc != nil {
		return c.wc.Close()
	}
	return nil
}

// forceClose marks the reader's coming error as deliberate, so Err stays nil.
func (c *conn) forceClose(r io.Closer) {
	c.mu.Lock()
	c.forced = true
	c.mu.Unlock()
	r.Close()
}

func (c *conn) setExit(err error) {
	c.mu.Lock()
	c.exitErr = err
	c.mu.Unlock()
}

func (c *conn) err() error {
	select {
	case <-c.done:
	default:
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exitErr != nil {
		return c.exitErr
	}
	return c.readErr
}

func isNullID(id json.RawMessage) bool {
	t := bytes.TrimSpace(id)
	return len(t) == 0 || string(t) == "null"
}

// idKey normalises an id for the pending map: a number and the same digits
// as a string are different ids, as JSON-RPC says.
func idKey(id json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, id); err != nil {
		return string(id)
	}
	return buf.String()
}
