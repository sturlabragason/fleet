package acphost

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
)

// Image attachments in the pane (ADR-125). Ctrl-V attaches the image on the
// clipboard; pasting or dropping the path of an image file attaches that
// file. An attachment rides in the prompt text as a token, the file's path
// between two attachMark bytes, so the queue, history and the prompt file
// keep working on strings. The token is a control byte that typing and
// pasting drop and other senders have stripped (stripMarks), so only the
// pane makes one. At send time blocks turns the tokens into ACP image
// blocks; everywhere a human or the agent reads the text, shown turns them
// into "[image: path]".

const attachMark = '\x1e'

// maxImage caps an image sent inline; a larger file goes as a link.
const maxImage = 8 << 20

// imageTypes are the image MIME types sent inline (what the models read).
var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// imageExts are the file name endings a pasted path must have to attach.
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

func attachToken(path string) string { return string(attachMark) + path + string(attachMark) }

// attachPath returns the path an attachment token names, ok false for
// anything else.
func attachPath(s string) (string, bool) {
	if len(s) < 3 || s[0] != attachMark || s[len(s)-1] != attachMark {
		return "", false
	}
	p := s[1 : len(s)-1]
	if strings.ContainsRune(p, attachMark) {
		return "", false
	}
	return p, true
}

// stripMarks removes attachment marks from text that did not come from the
// pane's own attach, so a prompt sent over the control socket can't attach
// a file.
func stripMarks(s string) string { return strings.ReplaceAll(s, string(attachMark), "") }

// splitAttach calls text for each run of plain text and file for each
// attachment token in s, in order. An unpaired mark is dropped.
func splitAttach(s string, text, file func(string)) {
	for {
		i := strings.IndexRune(s, attachMark)
		if i < 0 {
			break
		}
		j := strings.IndexRune(s[i+1:], attachMark)
		if j < 0 {
			s = s[:i] + s[i+1:]
			break
		}
		if i > 0 {
			text(s[:i])
		}
		file(s[i+1 : i+1+j])
		s = s[i+2+j:]
	}
	if s != "" {
		text(s)
	}
}

// shown is s as people and the agent's text tools read it: each attachment
// as "[image: path]".
func shown(s string) string {
	if !strings.ContainsRune(s, attachMark) {
		return s
	}
	var b strings.Builder
	splitAttach(s, func(t string) { b.WriteString(t) }, func(p string) { b.WriteString("[image: " + p + "]") })
	return b.String()
}

// blocks turns a prompt into ACP content blocks: text runs as text blocks,
// attachments as image blocks when the agent takes images and the file is
// a readable image of at most maxImage bytes, else as a resource_link the
// agent can open with its own tools. warn is told why an image went as a
// link.
func blocks(s string, caps acp.PromptCapabilities, warn func(string)) []acp.ContentBlock {
	if !strings.ContainsRune(s, attachMark) {
		return []acp.ContentBlock{acp.TextBlock(s)}
	}
	var out []acp.ContentBlock
	splitAttach(s, func(t string) {
		if strings.TrimSpace(t) != "" {
			out = append(out, acp.TextBlock(t))
		}
	}, func(p string) {
		uri := (&url.URL{Scheme: "file", Path: p}).String()
		link := func(why string) {
			warn(fmt.Sprintf("%s sent as a link: %s", filepath.Base(p), why))
			out = append(out, acp.ResourceLinkBlock(uri, filepath.Base(p)))
		}
		if !caps.Image {
			link("the agent takes no images")
			return
		}
		data, mime, err := readImage(p)
		if err != nil {
			link(err.Error())
			return
		}
		out = append(out, acp.ImageBlock(mime, data, uri))
	})
	if len(out) == 0 {
		out = append(out, acp.TextBlock(""))
	}
	return out
}

// readImage reads an image file and sniffs its type.
func readImage(p string) ([]byte, string, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, "", err
	}
	if fi.Size() > maxImage {
		return nil, "", fmt.Errorf("larger than %d MB", maxImage>>20)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	mime := http.DetectContentType(data)
	if !imageTypes[mime] {
		return nil, "", fmt.Errorf("not a png, jpeg, gif or webp image (%s)", mime)
	}
	return data, mime, nil
}

// pastedImages returns the image files a paste names: one or more paths,
// as a terminal pastes a dropped file (quoted, backslash-escaped or a
// file:// URI, space or newline separated). ok is false unless every word
// is an existing image file, so ordinary text is never taken for a path.
func pastedImages(s string) (paths []string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 8192 {
		return nil, false
	}
	words, ok := shellWords(s)
	if !ok || len(words) == 0 {
		return nil, false
	}
	for _, w := range words {
		p, ok := filePath(w)
		if !ok || !imageExts[strings.ToLower(filepath.Ext(p))] {
			return nil, false
		}
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			return nil, false
		}
		paths = append(paths, p)
	}
	return paths, true
}

// filePath reads one pasted word as an absolute file path: "/x", "~/x" or
// "file:///x".
func filePath(w string) (string, bool) {
	if rest, ok := strings.CutPrefix(w, "file://"); ok {
		u, err := url.Parse("file://" + rest)
		if err != nil || (u.Host != "" && u.Host != "localhost") {
			return "", false
		}
		w = u.Path
	} else if rest, ok := strings.CutPrefix(w, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		w = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(w) {
		return "", false
	}
	return filepath.Clean(w), true
}

// shellWords splits s at unquoted whitespace, honouring single and double
// quotes and backslash escapes; ok is false on an unclosed quote.
func shellWords(s string) (words []string, ok bool) {
	var cur strings.Builder
	in := false // inside a word
	var q rune  // the open quote, or 0
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case q != 0 && r == q:
			q = 0
		case q != 0:
			if r == '\\' && q == '"' {
				esc = true
				continue
			}
			cur.WriteRune(r)
		case r == '\\':
			esc, in = true, true
		case r == '\'' || r == '"':
			q, in = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if q != 0 || esc {
		return nil, false
	}
	if in {
		words = append(words, cur.String())
	}
	return words, true
}

// clipboardImage returns the image on the system clipboard as PNG, JPEG,
// GIF or WebP bytes, or the image file the clipboard names (a file copied
// in a file manager). A variable so tests can stub it.
var clipboardImage = func(ctx context.Context) (data []byte, path string, err error) {
	switch {
	case runtime.GOOS == "darwin":
		return darwinClipboard(ctx)
	case os.Getenv("WAYLAND_DISPLAY") != "" && have("wl-paste"):
		return pickClipboard(ctx, []string{"wl-paste", "--list-types"}, func(t string) []string {
			return []string{"wl-paste", "--no-newline", "--type", t}
		})
	case os.Getenv("DISPLAY") != "" && have("xclip"):
		return pickClipboard(ctx, []string{"xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"}, func(t string) []string {
			return []string{"xclip", "-selection", "clipboard", "-t", t, "-o"}
		})
	}
	return nil, "", errors.New("no clipboard tool: install wl-clipboard (Wayland) or xclip (X11)")
}

func have(tool string) bool { _, err := exec.LookPath(tool); return err == nil }

func output(ctx context.Context, argv []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
}

// pickClipboard lists the clipboard's types with list and reads the first
// image type with read, else a text/uri-list naming one image file.
func pickClipboard(ctx context.Context, list []string, read func(string) []string) ([]byte, string, error) {
	types, err := output(ctx, list)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", list[0], err)
	}
	var uris bool
	for t := range strings.FieldsSeq(string(types)) {
		if imageTypes[t] {
			data, err := output(ctx, read(t))
			if err != nil {
				return nil, "", fmt.Errorf("%s: %w", list[0], err)
			}
			return data, "", nil
		}
		uris = uris || t == "text/uri-list"
	}
	if uris {
		if b, err := output(ctx, read("text/uri-list")); err == nil {
			if ps, ok := pastedImages(strings.ReplaceAll(string(b), "\r", "")); ok && len(ps) == 1 {
				return nil, ps[0], nil
			}
		}
	}
	return nil, "", errors.New("the clipboard holds no image")
}

// darwinClipboard reads a PNG from the macOS clipboard with osascript,
// which prints it as «data PNGf<hex>».
func darwinClipboard(ctx context.Context) ([]byte, string, error) {
	out, err := output(ctx, []string{"osascript", "-e", "the clipboard as «class PNGf»"})
	if err != nil {
		return nil, "", errors.New("the clipboard holds no image")
	}
	s := strings.TrimSpace(string(out))
	s = strings.TrimSuffix(strings.TrimPrefix(s, "«data PNGf"), "»")
	data, err := hex.DecodeString(s)
	if err != nil {
		return nil, "", errors.New("the clipboard holds no image")
	}
	return data, "", nil
}

// saveImage writes clipboard image bytes to dir under a fresh name and
// returns the path. Files older than a week in dir are removed first.
func saveImage(dir string, data []byte) (string, error) {
	mime := http.DetectContentType(data)
	if !imageTypes[mime] {
		return "", fmt.Errorf("the clipboard image is %s, not png, jpeg, gif or webp", mime)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	prune(dir, 7*24*time.Hour)
	ext := "." + strings.TrimPrefix(mime, "image/")
	f, err := os.CreateTemp(dir, time.Now().Format("20060102-150405-")+"*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

func prune(dir string, age time.Duration) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && time.Since(fi.ModTime()) > age {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// AttachDir is where the pane keeps clipboard images for agent name.
func AttachDir(home, name string) string { return filepath.Join(home, "attachments", name) }
