package main

import "testing"

func TestParseControl(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line   string
		action string
		value  int64
		ok     bool
	}{
		{"toggle", "toggle", 0, true},
		{"  NEXT  ", "next", 0, true},
		{"seek -10000", "seek", -10000, true},
		{"seekto 62000", "seekto", 62000, true},
		{"seek", "", 0, false},
		{"seek soon", "", 0, false},
		{"rm -rf /", "", 0, false},
		{"", "", 0, false},
	}
	for _, c := range cases {
		got, ok := parseControl(c.line)
		if ok != c.ok {
			t.Fatalf("parseControl(%q) ok = %v, want %v", c.line, ok, c.ok)
		}
		if ok && (got.Action != c.action || got.Value != c.value) {
			t.Fatalf("parseControl(%q) = %+v, want %s/%d", c.line, got, c.action, c.value)
		}
	}
}
