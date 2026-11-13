package main

import (
	"flag"
	"reflect"
	"testing"
	"time"
)

func TestParseInterspersed(t *testing.T) {
	for _, c := range []struct {
		args, words []string
		n           int
	}{
		{[]string{"memory", "-n", "3", "survey"}, []string{"memory", "survey"}, 3},
		{[]string{"-n", "2", "a", "--", "-n", "5"}, []string{"a", "-n", "5"}, 2},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		n := fs.Int("n", 10, "")
		words, err := parseInterspersed(fs, c.args)
		if err != nil || !reflect.DeepEqual(words, c.words) || *n != c.n {
			t.Errorf("%v: words %v n %d err %v", c.args, words, *n, err)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"24h":        now.Add(-24 * time.Hour),
		"3d":         now.AddDate(0, 0, -3),
		"2026-11-10": time.Date(2026, 11, 10, 0, 0, 0, 0, time.Local),
	} {
		if got, err := parseSince(in, now); err != nil || !got.Equal(want) {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	if _, err := parseSince("soon", now); err == nil {
		t.Error("soon parsed")
	}
}
