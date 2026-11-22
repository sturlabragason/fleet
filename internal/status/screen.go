package status

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
)

// boxChars are box-drawing and block characters; a line made only of these
// (and spaces) is chrome.
const boxChars = "─━═│┃║╭╮╰╯╻╹╺╸╴╶▄▀▔▁▂▌▐█░▒▓┌┐└┘├┤┬┴┼╌╍┄┅┈┉╔╗╚╝╠╣╦╩╬"

// borders are stripped from both ends of a line.
const borders = "│┃║"

// CleanScreen drops pane chrome for a reader: lines made only of box or
// block characters, left/right borders and the transcript scrollbar,
// trailing spaces and repeated blank lines.
func CleanScreen(text string) string {
	var out []string
	for l := range strings.SplitSeq(text, "\n") {
		l = stripBorders(l)
		if strings.Trim(l, boxChars+" ") == "" {
			l = ""
		}
		if l == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func stripBorders(l string) string {
	for {
		t := strings.TrimRight(l, " ")
		if r := strings.TrimRight(t, borders); r != t {
			l = r
			continue
		}
		s := strings.TrimLeft(t, " ")
		if r := strings.TrimLeft(s, borders); r != s {
			l = strings.TrimPrefix(r, " ")
			continue
		}
		return t
	}
}

// scanTail calls fn with each whole line in the last LastMessageTail bytes
// of the file at path.
func scanTail(path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	off := max(fi.Size()-LastMessageTail, 0)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(f, 256<<10)
	if off > 0 { // drop the partial first line
		if _, err := r.ReadBytes('\n'); err != nil {
			return nil
		}
	}
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			fn(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
