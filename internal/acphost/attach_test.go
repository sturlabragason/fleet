package acphost

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/acp"
)

// pngBytes is a 1x1 PNG.
var pngBytes, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

func writePNG(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPastedImages(t *testing.T) {
	dir := t.TempDir()
	a := writePNG(t, dir, "shot one.png")
	b := writePNG(t, dir, "b.PNG")
	txt := filepath.Join(dir, "notes.txt")
	os.WriteFile(txt, []byte("hi"), 0o600)
	esc := strings.ReplaceAll(a, " ", `\ `)
	uri := "file://" + strings.ReplaceAll(a, " ", "%20")
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"'" + a + "' ", []string{a}},         // GNOME-style drop: quoted, trailing space
		{esc, []string{a}},                    // backslash-escaped
		{uri + "\n", []string{a}},             // file:// URI
		{`"` + a + `" ` + b, []string{a, b}},  // two dropped files
		{"look at " + b, nil},                 // a sentence, not paths
		{txt, nil},                            // not an image
		{filepath.Join(dir, "gone.png"), nil}, // missing
		{"shot.png", nil},                     // relative
		{"'" + a, nil},                        // unclosed quote
		{"file://otherhost" + b, nil},         // remote URI
	} {
		got, ok := pastedImages(tc.in)
		if ok != (tc.want != nil) || strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("pastedImages(%q) = %q, %v; want %q", tc.in, got, ok, tc.want)
		}
	}
}

func TestBlocksAndShown(t *testing.T) {
	dir := t.TempDir()
	img := writePNG(t, dir, "a.png")
	fake := filepath.Join(dir, "fake.png")
	os.WriteFile(fake, []byte("not an image"), 0o600)
	s := "see " + attachToken(img) + " and " + attachToken(fake)

	var warns []string
	warn := func(w string) { warns = append(warns, w) }
	bs := blocks(s, acp.PromptCapabilities{Image: true}, warn)
	if len(bs) != 4 || bs[0].Text != "see " || bs[2].Text != " and " {
		t.Fatalf("blocks = %+v", bs)
	}
	if bs[1].Type != "image" || bs[1].MimeType != "image/png" || bs[1].Data != base64.StdEncoding.EncodeToString(pngBytes) || bs[1].URI != "file://"+img {
		t.Fatalf("image block = %+v", bs[1])
	}
	if bs[3].Type != "resource_link" || bs[3].Name != "fake.png" || len(warns) != 1 {
		t.Fatalf("non-image = %+v, warns %q", bs[3], warns)
	}

	warns = nil
	bs = blocks(attachToken(img), acp.PromptCapabilities{}, warn)
	if len(bs) != 1 || bs[0].Type != "resource_link" || !strings.Contains(warns[0], "takes no images") {
		t.Fatalf("no image cap: %+v %q", bs, warns)
	}
	if bs := blocks("plain", acp.PromptCapabilities{Image: true}, warn); len(bs) != 1 || bs[0].Text != "plain" {
		t.Fatalf("plain = %+v", bs)
	}

	if got, want := shown(s), "see [image: "+img+"] and [image: "+fake+"]"; got != want {
		t.Fatalf("shown = %q", got)
	}
	if strings.ContainsRune(stripMarks(s), attachMark) {
		t.Fatal("stripMarks left a mark")
	}
}

func TestEditorAttach(t *testing.T) {
	var e editor
	e.paste("a\nb")
	e.attach("/x/one.png")
	e.insert(' ', 'h', 'i')
	e.attach("/x/two.png")
	var labels []string
	for _, r := range e.buf {
		if isPasteRune(r) {
			labels = append(labels, e.pasteLabel(r))
		}
	}
	if got := strings.Join(labels, ","); got != "[Paste #1 +2 lines],[Image #1 one.png],[Image #2 two.png]" {
		t.Fatalf("labels = %s", got)
	}
	text := e.text()
	var back editor
	back.insertText(text)
	if back.text() != text || len(back.pastes) != 2 {
		t.Fatalf("round trip: %q pastes %q", back.text(), back.pastes)
	}
}

// Dropping an image file on the pane and Ctrl-V both attach an image; the
// prompt goes out with image blocks and the transcript names the files.
func TestKeysAttachImages(t *testing.T) {
	dir := t.TempDir()
	dropped := writePNG(t, dir, "drop.png")
	clip := clipboardImage
	t.Cleanup(func() { clipboardImage = clip })
	clipboardImage = func(context.Context) ([]byte, string, error) { return pngBytes, "", nil }

	pr, pw := io.Pipe()
	defer pw.Close()
	r := startRig(t, Config{In: pr, Keys: true, Size: func() (int, int) { return 100, 20 }},
		acp.AgentCapabilities{PromptCapabilities: acp.PromptCapabilities{Image: true}})
	r.waitStatus("idle")
	io.WriteString(pw, "\x1b[200~'"+dropped+"' \x1b[201~\x16 what differs?")
	r.waitFor(func() bool {
		return strings.Contains(lastFrame(r.out.String()), "› [Image #1 drop.png][Image #2 ")
	}, "attachments in the box")
	io.WriteString(pw, "\r")
	r.waitFor(func() bool {
		c := r.conn.Calls()
		return len(c) > 0 && c[len(c)-1] == "prompt <image image/png><image image/png> what differs?"
	}, "image prompt")

	saved, _ := filepath.Glob(filepath.Join(AttachDir(r.home, "a1"), "*.png"))
	if len(saved) != 1 {
		t.Fatalf("clipboard images saved: %q", saved)
	}
	users := r.kinds(KindUser)
	if len(users) == 0 || users[len(users)-1].Text != "[image: "+dropped+"][image: "+saved[0]+"] what differs?" {
		t.Fatalf("transcript = %+v", users)
	}

	// A prompt from fleet can't attach a file.
	if _, err := r.call(Request{Op: OpPrompt, Text: "x" + attachToken(dropped)}); err != nil {
		t.Fatal(err)
	}
	r.conn.turns <- func(handler) acp.StopReason { return acp.StopEndTurn }
	r.waitFor(func() bool {
		c := r.conn.Calls()
		return len(c) > 0 && c[len(c)-1] == "prompt x"+dropped
	}, "stripped prompt")
}
