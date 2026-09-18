package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/ytdlp"
)

func TestYtdlpVerdict(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		version string
		want    Status
		detail  string
	}{
		{"2026.08.19", OK, "30 days old"},
		{"2026.09.18", OK, "released today"},
		{"2026.06.01", Warn, "109 days old"},
		{"2025.01.02", Warn, "likely to fail"},
		{"2026.08.19.232255", OK, "30 days old"}, // nightly build suffix
		{"custom-build", OK, "custom-build"},
	}
	for _, tt := range tests {
		got, detail := ytdlpVerdict(tt.version, now)
		if got != tt.want || !strings.Contains(detail, tt.detail) {
			t.Errorf("ytdlpVerdict(%q) = %s %q, want %s containing %q", tt.version, got, detail, tt.want, tt.detail)
		}
	}
}

func TestParseEncoders(t *testing.T) {
	out := `Encoders:
 V..... = Video
 ------
 V....D libx264              libx264 H.264
 A....D flac                 FLAC (Free Lossless Audio Codec)
 A....D libopus              libopus Opus (codec opus)
`
	have := parseEncoders(out)
	if !have["flac"] || !have["libopus"] || have["libmp3lame"] || have["libx264"] {
		t.Errorf("got %v", have)
	}
}

func TestFFmpegVersion(t *testing.T) {
	for in, want := range map[string]string{
		"ffmpeg version n9.0.1 Copyright (c) 2000-2026": "9.0.1",
		"ffprobe version 6.1.1-3ubuntu5 Copyright":      "6.1.1-3ubuntu5",
		"something else entirely":                       "unknown version",
		"":                                              "unknown version",
	} {
		if got := ffmpegVersion(in); got != want {
			t.Errorf("ffmpegVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[uint64]string{512: "512 B", 1536: "1.5 KB", 291 << 30: "291.0 GB", 5 << 40: "5.0 TB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestYoutubeFix(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{ytdlp.ErrToolMissing, "install yt-dlp"},
		{errors.New("yt-dlp: [youtube] x: Sign in to confirm you're not a bot"), "cookies_from_browser"},
		{fmt.Errorf("download failed: %w", ytdlp.ErrBotCheck), "cookies_from_browser"},
		{errors.New("yt-dlp: HTTP Error 403: Forbidden"), "update it"},
		{context.DeadlineExceeded, "timed out"},
		{errors.New("dial tcp: no such host"), "internet connection"},
	}
	for _, tt := range tests {
		if got := youtubeFix(tt.err); !strings.Contains(got, tt.want) {
			t.Errorf("youtubeFix(%v) = %q, want it to mention %q", tt.err, got, tt.want)
		}
	}
}

func TestCheckLibrary(t *testing.T) {
	dir := t.TempDir()
	if c := checkLibrary(dir); c.Status != OK || !strings.Contains(c.Detail, "free") {
		t.Errorf("existing dir: %+v", c)
	}
	if c := checkLibrary(filepath.Join(dir, "new", "music")); c.Status != OK || !strings.Contains(c.Detail, "will be created") {
		t.Errorf("missing dir: %+v", c)
	}
	locked := filepath.Join(dir, "locked")
	os.Mkdir(locked, 0o500)
	defer os.Chmod(locked, 0o700)
	if os.Getuid() != 0 { // root writes anywhere
		if c := checkLibrary(locked); c.Status != Fail || c.Fix == "" {
			t.Errorf("read-only dir: %+v", c)
		}
	}
}

func TestCheckIndex(t *testing.T) {
	dir := t.TempDir()
	if c := checkIndex(filepath.Join(dir, "none.json")); c.Status != OK || !strings.Contains(c.Detail, "not created yet") {
		t.Errorf("fresh: %+v", c)
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	if c := checkIndex(bad); c.Status != Fail || !strings.Contains(c.Fix, "delete") {
		t.Errorf("corrupt: %+v", c)
	}
	song := filepath.Join(dir, "a.opus")
	os.WriteFile(song, []byte("x"), 0o600)
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{"tracks":{"a.opus":{"path":"`+song+`"},"b.opus":{"path":"`+filepath.Join(dir, "gone.opus")+`"}}}`), 0o600)
	if c := checkIndex(good); c.Status != OK || !strings.Contains(c.Detail, "2 songs known, 1 point") {
		t.Errorf("with a deleted file: %+v", c)
	}
}

// fakeTool writes an executable that prints byArg's entry for whichever key
// appears in its first two arguments, and nothing otherwise.
func fakeTool(t *testing.T, dir, name string, byArg map[string]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\ncase \"$1 $2\" in\n")
	for arg, out := range byArg {
		b.WriteString("  *" + arg + "*) cat <<'EOF'\n" + out + "\nEOF\n  ;;\n")
	}
	b.WriteString("esac\n")
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunOffline(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Output = filepath.Join(dir, "Music")
	cfg.IndexPath = filepath.Join(dir, "index.json")
	cfg.Tools.YtDlp = fakeTool(t, dir, "yt-dlp", map[string]string{"--version": "2025.01.02"})
	cfg.Tools.FFmpeg = fakeTool(t, dir, "ffmpeg", map[string]string{
		"-version":  "ffmpeg version n9.0.1 Copyright",
		"-encoders": " A....D flac                 FLAC\n A....D libmp3lame           MP3",
	})
	cfg.Tools.FFprobe = filepath.Join(dir, "no-ffprobe")

	checks := Run(context.Background(), Env{
		Config: cfg, ConfigPath: filepath.Join(dir, "config.toml"),
		Offline: true, Now: time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC),
	})
	byName := map[string]Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	expect := map[string]Status{
		"yt-dlp":  Warn, // 259 days old
		"ffmpeg":  Fail, // no libopus, and the format is opus
		"ffprobe": Warn, // missing, only needed for index rebuilds
		"config":  OK,
		"library": OK,
		"index":   OK,
		"network": Skip,
	}
	for name, want := range expect {
		if got := byName[name]; got.Status != want {
			t.Errorf("%s: %s (%q), want %s", name, got.Status, got.Detail, want)
		}
	}
	if !strings.Contains(byName["ffmpeg"].Detail, "opus ✗ mp3 ✓ flac ✓") {
		t.Errorf("ffmpeg detail %q", byName["ffmpeg"].Detail)
	}
	if Healthy(checks) {
		t.Error("healthy despite the ffmpeg failure")
	}

	checks = Run(context.Background(), Env{Config: cfg, ConfigErr: errors.New("bad key"), ConfigPath: "c.toml", Offline: true, Now: time.Now()})
	for _, c := range checks {
		if c.Name == "config" && (c.Status != Fail || !strings.Contains(c.Fix, "c.toml")) {
			t.Errorf("broken config: %+v", c)
		}
	}
}
