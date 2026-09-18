package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/config"
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
	r.Clean, r.Explicit = false, true
	if got, want := resultLabel(r), "Blinding Lights — The Weeknd (2019) 3:20 [E]"; got != want {
		t.Errorf("explicit: got %q, want %q", got, want)
	}
}

func TestConfirmDownload(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"\n", true},
		{"y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"q\n", false},
		{"maybe\nn\n", false}, // unclear answers ask again
		{"maybe\n\n", true},
		{"", false}, // end of input never downloads
	}
	for _, tt := range tests {
		var out strings.Builder
		if got := confirmDownload(strings.NewReader(tt.input), &out, []string{"Song — A", "Other — B"}); got != tt.want {
			t.Errorf("input %q: got %v, want %v", tt.input, got, tt.want)
		}
		if !strings.Contains(out.String(), "  • Song — A") || !strings.Contains(out.String(), "Download 2 songs? [Y/n]") {
			t.Errorf("prompt:\n%s", out.String())
		}
	}
}

func TestPseudoVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.0.0-20260918105524-80f7f43ed8ab+dirty": true,
		"v0.1.1-0.20260918105524-80f7f43ed8ab":     true,
		"v0.1.0":                                   false,
		"v1.2.3-rc.1":                              false,
	} {
		if got := pseudoVersion.MatchString(v); got != want {
			t.Errorf("pseudoVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestReadLinks(t *testing.T) {
	in := `https://open.spotify.com/track/4uJSCrI7r0usNJ3aaHAuC6
# a comment

https://open.spotify.com/track/4ceTCJPLBQBOogseivMuhL   spotify:track:7LVHVU3tWfcxj5aiPFEW4Q
`
	got, err := readLinks(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://open.spotify.com/track/4uJSCrI7r0usNJ3aaHAuC6",
		"https://open.spotify.com/track/4ceTCJPLBQBOogseivMuhL",
		"spotify:track:7LVHVU3tWfcxj5aiPFEW4Q",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestResolveListRejectsNonSongs(t *testing.T) {
	cfg := config.Default()
	for _, links := range [][]string{
		{"https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M"},
		{"https://open.spotify.com/track/4uJSCrI7r0usNJ3aaHAuC6", "hello"},
	} {
		if _, err := resolveList(context.Background(), cfg, links, func(string, int, int) {}); err == nil {
			t.Errorf("%v: no error", links)
		}
	}
	// A podcast episode copied along with songs is skipped, not an error.
	if got, err := resolveList(context.Background(), cfg, []string{"spotify:episode:0Q86acNRm6V9GYx55SXKwf"}, func(string, int, int) {}); err != nil || len(got) != 0 {
		t.Errorf("episode: %v, %v", got, err)
	}
}

func TestBriefHandler(t *testing.T) {
	var out strings.Builder
	setupLogging(&out, false)
	slog.Info("hidden")
	slog.Warn("skipping a track Spotify no longer has", "id", "abc")
	if got, want := out.String(), "warning: skipping a track Spotify no longer has (id=abc)\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConfigInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "geet", "config.toml")
	var out, errOut bytes.Buffer
	if code := run([]string{"config", "init", "--config", path}, &out, &errOut); code != exitOK {
		t.Fatalf("init: exit %d: %s", code, errOut.String())
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "# jobs = 4") {
		t.Fatalf("file not written with the template: %v", err)
	}

	// The user's settings are never replaced silently.
	if err := os.WriteFile(path, []byte("jobs = 16\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := run([]string{"config", "init", "--config", path}, &out, &errOut); code == exitOK || !strings.Contains(errOut.String(), "already exists") {
		t.Errorf("init over an existing file: exit %d, %q", code, errOut.String())
	}
	if b, _ := os.ReadFile(path); string(b) != "jobs = 16\n" {
		t.Errorf("existing file changed: %q", b)
	}
	if code := run([]string{"config", "init", "--config", path, "--force"}, &out, &errOut); code != exitOK {
		t.Fatalf("init --force: exit %d", code)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "# jobs = 4") {
		t.Error("--force didn't write the template")
	}
}
