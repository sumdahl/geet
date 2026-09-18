package download

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sumdahl/geet/internal/ytdlp"
)

func TestFetch(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-yt-dlp")
	args := filepath.Join(dir, "args")
	// Mimics yt-dlp: writes the file named by --output and prints the
	// --print template's fields.
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + args + `"
echo "GEET-PROGRESS 1024 4096 NA"
echo "GEET-PROGRESS 2048 NA 4100.5"
echo "GEET-PROGRESS 4096 4096 NA"
printf '%s\topus\t152.303\n' "` + filepath.Join(dir, "source.webm") + `"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var progress [][2]int64
	got, err := Fetch(context.Background(), ytdlp.Runner{Binary: bin}, "https://www.youtube.com/watch?v=abc", dir,
		func(done, total int64) { progress = append(progress, [2]int64{done, total}) })
	if err != nil {
		t.Fatal(err)
	}
	if want := [][2]int64{{1024, 4096}, {2048, 4100}, {4096, 4096}}; !reflect.DeepEqual(progress, want) {
		t.Errorf("progress %v, want %v", progress, want)
	}
	want := Source{Path: filepath.Join(dir, "source.webm"), Codec: "opus", Kbps: 152.303}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	raw, _ := os.ReadFile(args)
	gotArgs := strings.Split(strings.TrimSpace(string(raw)), "\n")
	wantArgs := []string{
		"--format", "bestaudio[acodec=opus]/bestaudio",
		"--no-playlist",
		"--progress", "--newline",
		"--progress-template", "download:GEET-PROGRESS %(progress.downloaded_bytes)s %(progress.total_bytes)s %(progress.total_bytes_estimate)s",
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
