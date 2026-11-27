package conf

import (
	"reflect"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	d, err := Parse(`
top = "x" # comment
[claude-work]
command = "claude"
args = ["--model", "claude-sonnet-5"]
env = { CLAUDE_CONFIG_DIR = "/h/.claude-work", A = 'b' }
waiting = ['\[y/n\]', "Do you # want", '\[Paste #\d+'] # comment
`)
	if err != nil {
		t.Fatal(err)
	}
	s := d["claude-work"]
	if Str(d[""], "top") != "x" || Str(s, "command") != "claude" {
		t.Fatalf("%v", d)
	}
	if !reflect.DeepEqual(List(s, "args"), []string{"--model", "claude-sonnet-5"}) {
		t.Fatalf("args %v", List(s, "args"))
	}
	if Map(s, "env")["CLAUDE_CONFIG_DIR"] != "/h/.claude-work" || Map(s, "env")["A"] != "b" {
		t.Fatalf("env %v", Map(s, "env"))
	}
	if !reflect.DeepEqual(List(s, "waiting"), []string{`\[y/n\]`, "Do you # want", `\[Paste #\d+`}) {
		t.Fatalf("waiting %v", List(s, "waiting"))
	}
	if _, err := Parse("bad line"); err == nil {
		t.Fatal("want error")
	}
}

// TestParseErrors: what real TOML refuses is a parse error, not a value, so
// a caller keeps its last good state instead of loading a broken value.
func TestParseErrors(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		bad      bool
	}{
		{"unterminated basic string", "a = \"#00ff00\n", true},
		{"unterminated literal string", "a = '#00ff00\n", true},
		{"lone quote", "a = \"\n", true},
		{"escaped closing quote", `a = "x\"` + "\n", true},
		{"duplicate key", "a = \"1\"\na = \"2\"\n", true},
		{"duplicate key in a reopened section", "[s]\na = \"1\"\n[t]\n[s]\na = \"2\"\n", true},
		{"same key in two sections", "a = \"1\"\n[s]\na = \"2\"\n", false},
		{"escaped backslash closes", `a = "C:\\"` + "\n", false},
		{"empty string", "a = \"\"\nb = ''\n", false},
		{"bare value", "a = 3\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.in); (err != nil) != tc.bad {
				t.Errorf("Parse(%q) err = %v, want error %v", tc.in, err, tc.bad)
			}
		})
	}
}

func TestBoolAndDur(t *testing.T) {
	d, err := Parse("yes = true\nno = False\nnum = 3\nt = \"5s\"\nbad = \"fortnight\"\n")
	if err != nil {
		t.Fatal(err)
	}
	m := d[""]
	for _, c := range []struct {
		key     string
		val, ok bool
	}{
		{"yes", true, true}, {"no", false, true}, {"num", false, false}, {"missing", false, false},
	} {
		v, ok := Bool(m, c.key)
		if v != c.val || ok != c.ok {
			t.Errorf("Bool(%s) = %v,%v want %v,%v", c.key, v, ok, c.val, c.ok)
		}
	}
	if v, ok := Dur(m, "t"); !ok || v != 5*time.Second {
		t.Errorf("Dur(t) = %v,%v", v, ok)
	}
	for _, k := range []string{"bad", "missing"} {
		if _, ok := Dur(m, k); ok {
			t.Errorf("Dur(%s) should fail", k)
		}
	}
}

func TestParseMultiLine(t *testing.T) {
	for _, c := range []struct {
		name, in string
		key      string
		want     any
	}{
		{"array split over two lines", "[kagent-x]\nacp = [\"--context\", \"x\",\n  \"--agent\", \"kagent/claude\"]\nafter = \"y\"\n",
			"acp", []string{"--context", "x", "--agent", "kagent/claude"}},
		{"one item per line, comments, trailing comma", "[p]\nargs = [\n  \"a\", # first\n  '[b]',\n]\n",
			"args", []string{"a", "[b]"}},
		{"inline table over lines", "[p]\nenv = {\n  A = \"1\",\n  B = '2' }\n",
			"env", map[string]string{"A": "1", "B": "2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Parse(c.in)
			if err != nil {
				t.Fatal(err)
			}
			for _, sec := range d {
				if v, ok := sec[c.key]; ok && !reflect.DeepEqual(v, c.want) {
					t.Fatalf("%s = %#v, want %#v", c.key, v, c.want)
				} else if ok {
					return
				}
			}
			t.Fatalf("%s missing in %v", c.key, d)
		})
	}
	if _, err := Parse("a = 1\nb = [\"x\",\n\"y\"\n"); err == nil || err.Error() != "line 2: unterminated array" {
		t.Fatalf("err = %v, want line 2: unterminated array", err)
	}
}
