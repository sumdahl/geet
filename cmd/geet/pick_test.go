package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/spotify"
)

func TestParseChoice(t *testing.T) {
	tests := []struct {
		in       string
		want     []int
		quit     bool
		wantsErr bool
	}{
		{in: "", want: []int{0}},
		{in: "1", want: []int{0}},
		{in: " 3 ", want: []int{2}},
		{in: "1 3", want: []int{0, 2}},
		{in: "1,3", want: []int{0, 2}},
		{in: "2-4", want: []int{1, 2, 3}},
		{in: "3 1 3", want: []int{2, 0}},
		{in: "q", quit: true},
		{in: "Q", quit: true},
		{in: "0", wantsErr: true},
		{in: "6", wantsErr: true},
		{in: "4-2", wantsErr: true},
		{in: "abc", wantsErr: true},
		{in: ",", wantsErr: true},
	}
	for _, tt := range tests {
		got, quit, err := parseChoice(tt.in, 5)
		if (err != nil) != tt.wantsErr || quit != tt.quit || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseChoice(%q) = %v, %v, %v; want %v, %v, err=%v", tt.in, got, quit, err, tt.want, tt.quit, tt.wantsErr)
		}
	}
}

func TestPickList(t *testing.T) {
	var out strings.Builder
	got, err := pickList(strings.NewReader("9\n2 3\n"), &out, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("got %v, want [1 2] after re-asking", got)
	}
	if !strings.Contains(out.String(), "1. a") || !strings.Contains(out.String(), `"9" isn't a number`) {
		t.Errorf("output:\n%s", out.String())
	}
	if got, _ := pickList(strings.NewReader(""), &out, []string{"a"}); got != nil {
		t.Errorf("EOF picked %v", got)
	}
}

// fakeFzf writes a stand-in for fzf that picks input lines 1 and 3, or
// exits with $FAKE_FZF_EXIT to simulate Esc.
func fakeFzf(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fzf")
	script := `#!/bin/sh
[ -n "$FAKE_FZF_EXIT" ] && exit "$FAKE_FZF_EXIT"
sed -n '1p;3p'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestPickFzf(t *testing.T) {
	bin := fakeFzf(t)
	labels := []string{"Same — A", "Other — B", "Same — A"}
	got, err := pickFzf(context.Background(), bin, "q", labels)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{0, 2}) {
		t.Errorf("got %v, want [0 2] (identical labels told apart by index)", got)
	}

	for _, code := range []string{"130", "1"} {
		t.Setenv("FAKE_FZF_EXIT", code)
		got, err := pickFzf(context.Background(), bin, "q", labels)
		if err != nil || got != nil {
			t.Errorf("exit %s: got %v, %v; want nothing picked", code, got, err)
		}
	}
	t.Setenv("FAKE_FZF_EXIT", "2")
	if _, err := pickFzf(context.Background(), bin, "q", labels); err == nil {
		t.Error("exit 2 (fzf error) not reported")
	}
}

func TestPickResultsNeedsTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := pickResults(context.Background(), "auto", "q", []itunes.Result{{}}, f, &strings.Builder{}); err != errNoTerminal {
		t.Errorf("err = %v, want errNoTerminal", err)
	}
}

func TestResultLabel(t *testing.T) {
	r := itunes.Result{Track: spotify.Track{
		Title: "Blinding Lights", Artists: []string{"The Weeknd"}, Album: "After Hours", Year: 2019, Duration: 200046 * time.Millisecond,
	}}
	if got, want := resultLabel(r), "Blinding Lights — The Weeknd · After Hours (2019) 3:20"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	r.Album = "Blinding Lights - Single"
	r.Clean = true
	if got, want := resultLabel(r), "Blinding Lights — The Weeknd (2019) 3:20 (clean)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
