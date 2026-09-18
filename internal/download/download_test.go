package download

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sumdahl/spotify-dl/internal/ytdlp"
)

func TestFetch(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-yt-dlp")
	args := filepath.Join(dir, "args")
	// Mimics yt-dlp: writes the file named by --output and prints the
	// --print template's fields.
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + args + `"
echo "[download] Destination: ignored progress line"
printf '%s\topus\t152.303\n' "` + filepath.Join(dir, "source.webm") + `"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Fetch(context.Background(), ytdlp.Runner{Binary: bin}, "https://www.youtube.com/watch?v=abc", dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Source{Path: filepath.Join(dir, "source.webm"), Codec: "opus", Kbps: 152.303}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	raw, _ := os.ReadFile(args)
	gotArgs := strings.Split(strings.TrimSpace(string(raw)), "\n")
	wantArgs := []string{
		"--format", "bestaudio[acodec=opus]/bestaudio",
		"--no-playlist", "--no-warnings", "--no-progress",
		"--output", filepath.Join(dir, "source.%(ext)s"),
		"--print", "after_move:%(filepath)s\t%(acodec)s\t%(abr)s",
		"--", "https://www.youtube.com/watch?v=abc",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args\n got  %q\n want %q", gotArgs, wantArgs)
	}
}

func TestParsePrint(t *testing.T) {
	tests := []struct {
		out     string
		want    Source
		wantErr bool
	}{
		{out: "/t/source.m4a\tmp4a.40.2\t129.5\n", want: Source{Path: "/t/source.m4a", Codec: "mp4a.40.2", Kbps: 129.5}},
		{out: "/t/source.webm\tNA\tNA\n", want: Source{Path: "/t/source.webm"}},
		{out: "", wantErr: true},
		{out: "garbage", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parsePrint(tt.out)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parsePrint(%q) = %+v, %v", tt.out, got, err)
		}
	}
}
