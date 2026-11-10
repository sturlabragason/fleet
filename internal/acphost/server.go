package acphost

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// maxSocketPath is the longest unix socket path that works everywhere:
// sun_path is 104 bytes on macOS and the BSDs (108 on Linux), NUL included.
const maxSocketPath = 103

// listen opens the control socket at path, 0600 in a 0700 dir. A socket
// nobody answers on is a stale one from a host that died and is removed;
// one that answers means a host already runs for this agent.
func listen(path string) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("acp-host: socket path too long (%d bytes, max %d): %s; use a shorter FLEET_HOME", len(path), maxSocketPath, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if c, err := net.DialTimeout("unix", path, 500*time.Millisecond); err == nil {
		c.Close()
		return nil, fmt.Errorf("acp-host: %s: a host is already running", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func (h *host) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go h.serveConn(c)
	}
}

// serveConn answers request lines until the peer closes or stops the host.
func (h *host) serveConn(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		_ = c.SetReadDeadline(time.Now().Add(time.Minute))
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req Request
		resp := Response{}
		if err := json.Unmarshal(line, &req); err != nil {
			resp.Err = "bad request: " + err.Error()
		} else {
			resp = h.handle(req)
		}
		b, _ := json.Marshal(resp)
		_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, werr := c.Write(append(b, '\n'))
		if req.Op == OpStop && resp.OK {
			h.requestStop()
			return
		}
		if werr != nil {
			return
		}
	}
}

// handle runs one control request.
func (h *host) handle(req Request) Response {
	var err error
	switch req.Op {
	case OpPrompt:
		if req.Text == "" {
			err = errors.New("empty prompt")
		} else {
			err = h.prompt(req.Text)
		}
	case OpAnswer:
		err = h.answer(req.Option, req.OptionID)
	case OpCancel:
		err = h.cancel()
	case OpState, OpStop:
	default:
		err = fmt.Errorf("unknown op %q", req.Op)
	}
	resp := h.snapshot()
	if err != nil {
		resp.OK, resp.Err = false, err.Error()
	}
	return resp
}
