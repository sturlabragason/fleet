package mcp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sturlabragason/fleet/internal/rules"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// pageBytes is one page of fleet_brief file=…, well under the size at which
// an MCP client saves a reply to a file again.
const pageBytes = 8 << 10

// spillMark is in the name of every file Copilot CLI saves a long tool reply
// to; fleet_brief file=… reads only those, and only under the temp dir.
const spillMark = "copilot-tool-output"

// pinnedFile is the file= name for every pinned note.
const pinnedFile = "pinned"

// listFiles are the file= names fleet_brief reads from fleet's own state
// rather than a saved tool reply: every pinned note, every open item ranked
// as the brief ranks them, and the standing rules every worker gets
// (ADR-188: the admiral got none of them).
var listFiles = map[string]struct {
	name  string
	lines func(s *Server) []string
}{
	pinnedFile: {"pinned notes", func(s *Server) []string { return supervisor.Pinned(s.F.Home) }},
	"todo":     {"open items", (*Server).todoLines},
	"rules":    {"standing user rules", func(s *Server) []string { r, _ := rules.List(rules.UserFile(s.F.Home)); return r }},
}

// readFile is fleet_brief file=…: a page of a saved tool reply or of a
// listFiles list from byte offset, or the lines matching query.
func (s *Server) readFile(a args) (string, error) {
	r, size, closeFn, err := s.openFile(a.File)
	if err != nil {
		return "", err
	}
	defer closeFn()
	name := a.File
	if l, ok := listFiles[name]; ok {
		name = l.name
	}
	if a.Query != "" {
		return grepPage(name, io.NewSectionReader(r, 0, size), a.Query)
	}
	return page(name, r, size, a.Offset)
}

func (s *Server) openFile(file string) (io.ReaderAt, int64, func(), error) {
	if l, ok := listFiles[file]; ok {
		text := ""
		if ls := l.lines(s); len(ls) > 0 {
			text = strings.Join(ls, "\n") + "\n"
		}
		return strings.NewReader(text), int64(len(text)), func() {}, nil
	}
	path, err := spillPath(file)
	if err != nil {
		return nil, 0, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("read %s: %w", file, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, nil, fmt.Errorf("read %s: %w", file, err)
	}
	return f, st.Size(), func() { _ = f.Close() }, nil
}

// spillPath is file resolved, when it is a saved tool reply under the temp
// dir; anything else is refused, so the admiral can't read other files.
func spillPath(file string) (string, error) {
	refuse := fmt.Errorf("file must be pinned, todo, rules or the path of a saved tool reply (a *%s* file in %s)", spillMark, os.TempDir())
	if !filepath.IsAbs(file) {
		return "", refuse
	}
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", file, err)
	}
	if !strings.Contains(filepath.Base(resolved), spillMark) {
		return "", refuse
	}
	// The MCP server inherits the client's TMPDIR, so this is where it saves.
	root, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", fmt.Errorf("temp dir: %w", err)
	}
	if rel, err := filepath.Rel(root, resolved); err != nil || !filepath.IsLocal(rel) {
		return "", refuse
	}
	// The temp dir is shared: a FIFO or device there would block the server.
	if fi, err := os.Stat(resolved); err != nil || !fi.Mode().IsRegular() {
		return "", refuse
	}
	return resolved, nil
}

// page is up to pageBytes of r from byte offset off, ending on a line
// break when the page has one, with the offset of the next page.
func page(name string, r io.ReaderAt, size int64, off int) (string, error) {
	if off < 0 || int64(off) > size {
		return fmt.Sprintf("%s is %d bytes; offset %d is outside it\n", name, size, off), nil
	}
	// A few bytes before off and after the page let both ends land on a
	// rune start.
	from := max(int64(off)-utf8.UTFMax, 0)
	buf := make([]byte, min(int64(off)+pageBytes+utf8.UTFMax, size)-from)
	if _, err := r.ReadAt(buf, from); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	start := int(int64(off) - from)
	for start > 0 && start < len(buf) && !utf8.RuneStart(buf[start]) {
		start--
	}
	end := min(start+pageBytes, len(buf))
	if from+int64(end) < size {
		if nl := bytes.LastIndexByte(buf[start:end], '\n'); nl > 0 {
			end = start + nl + 1
		} else {
			for end > start && !utf8.RuneStart(buf[end]) {
				end--
			}
		}
	}
	at, next := from+int64(start), from+int64(end)
	head := fmt.Sprintf("%s, bytes %d-%d of %d", name, at, next, size)
	if next < size {
		head += fmt.Sprintf("; next page: offset=%d", next)
	} else {
		head += "; end"
	}
	return head + "\n" + string(buf[start:end]), nil
}

// grepPage is the lines of r that contain query (case-insensitive), each
// with its line number and byte offset, up to pageBytes.
func grepPage(name string, r io.Reader, query string) (string, error) {
	q := strings.ToLower(query)
	br := bufio.NewReader(r)
	var b strings.Builder
	n, shown, off := 0, 0, 0
	for i := 1; ; i++ {
		line, err := br.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		at := off
		off += len(line)
		if strings.Contains(strings.ToLower(line), q) {
			n++
			line = strings.TrimRight(line, "\n")
			if rs := []rune(line); len(rs) > 400 {
				line = string(rs[:400]) + "…"
			}
			if entry := fmt.Sprintf("%d (offset=%d): %s\n", i, at, line); b.Len()+len(entry) <= pageBytes {
				b.WriteString(entry)
				shown++
			}
		}
		if err != nil {
			break
		}
	}
	head := fmt.Sprintf("%s: %d lines match %q", name, n, query)
	if shown < n {
		head += fmt.Sprintf(", first %d shown; narrow the query or page with offset", shown)
	}
	return head + "\n" + b.String(), nil
}
