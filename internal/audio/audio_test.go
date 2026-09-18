package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/spotify-dl/internal/spotify"
)

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
}

// makeSource writes a 2s tone as Opus in WebM, the shape YouTube serves.
func makeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "source.webm")
	out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:a", "libopus", "-b:a", "96k", src).CombinedOutput()
	if err != nil {
		t.Fatalf("making source: %v: %s", err, out)
	}
	return src
}

func makeCover(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(0, 0, color.White)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type probe struct {
	Streams []struct {
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		Width       int               `json:"width"`
		Tags        map[string]string `json:"tags"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		BitRate string            `json:"bit_rate"`
		Tags    map[string]string `json:"tags"`
	} `json:"format"`
}

func ffprobe(t *testing.T, path string) probe {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var p probe
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// tags merges format and audio-stream tags with lowercase keys: where a tag
// lands differs by container (Ogg keeps them on the stream).
func (p probe) tags() map[string]string {
	m := map[string]string{}
	add := func(tags map[string]string) {
		for k, v := range tags {
			m[strings.ToLower(k)] = v
		}
	}
	add(p.Format.Tags)
	for _, s := range p.Streams {
		if s.CodecType == "audio" {
			add(s.Tags)
		}
	}
	return m
}

func TestEncodeRoundTrip(t *testing.T) {
	requireTools(t)
	src := makeSource(t)
	cover := makeCover(t)
	track := spotify.Track{
		ID: "4rXLjWdF2ZZpXCVTfWcshS", Title: "fukumean; #1 = hit", Artists: []string{"Gunna", "सज्जन राज वैद्य"},
		AlbumArtist: "Gunna", Album: "a Gift & a Curse", TrackNumber: 6, DiscNumber: 1, Year: 2023,
		ISRC: "USAT22300001", Duration: 2 * time.Second,
	}

	tests := []struct {
		format, bitrate string
		wantCodec       string
		isrcKey         string
		check           func(t *testing.T, p probe)
	}{
		{format: "opus", wantCodec: "opus", isrcKey: "isrc", check: func(t *testing.T, p probe) {
			// No bitrate and an Opus source: copied, so still ~96k, not re-encoded to 160k.
			if br, _ := strconv.Atoi(p.Format.BitRate); br > 130_000 {
				t.Errorf("bit rate %d: source was re-encoded instead of copied", br)
			}
		}},
		{format: "opus", bitrate: "64k", wantCodec: "opus", isrcKey: "isrc", check: func(t *testing.T, p probe) {
			if br, _ := strconv.Atoi(p.Format.BitRate); br > 85_000 {
				t.Errorf("bit rate %d: 64k was not applied", br)
			}
		}},
		{format: "mp3", bitrate: "192k", wantCodec: "mp3", isrcKey: "tsrc", check: func(t *testing.T, p probe) {
			if br, _ := strconv.Atoi(p.Format.BitRate); br < 150_000 {
				t.Errorf("bit rate %d: 192k was not applied", br)
			}
		}},
		{format: "flac", wantCodec: "flac", isrcKey: "isrc"},
	}
	for _, tt := range tests {
		t.Run(tt.format+tt.bitrate, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "out."+tt.format)
			err := Encode(context.Background(), "ffmpeg", Job{
				Source: src, SourceCodec: "opus", Dest: dest, Format: tt.format, Bitrate: tt.bitrate,
				Track: track, Cover: cover,
			})
			if err != nil {
				t.Fatal(err)
			}
			p := ffprobe(t, dest)

			tags := p.tags()
			want := map[string]string{
				"title": track.Title, "artist": "Gunna, सज्जन राज वैद्य", "album_artist": "Gunna",
				"album": "a Gift & a Curse", "track": "6", "disc": "1", "date": "2023",
				tt.isrcKey: track.ISRC, "comment": "https://open.spotify.com/track/4rXLjWdF2ZZpXCVTfWcshS",
			}
			for k, v := range want {
				if tags[k] != v {
					t.Errorf("tag %s = %q, want %q", k, tags[k], v)
				}
			}

			var audioCodec string
			var coverWidth int
			for _, s := range p.Streams {
				switch s.CodecType {
				case "audio":
					audioCodec = s.CodecName
				case "video":
					coverWidth = s.Width
				}
			}
			if audioCodec != tt.wantCodec {
				t.Errorf("codec %q, want %q", audioCodec, tt.wantCodec)
			}
			if coverWidth != 64 {
				t.Errorf("cover width %d, want 64 (embedded cover missing or wrong)", coverWidth)
			}
			if tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

func TestEncodeWithoutCover(t *testing.T) {
	requireTools(t)
	dest := filepath.Join(t.TempDir(), "out.mp3")
	err := Encode(context.Background(), "ffmpeg", Job{
		Source: makeSource(t), SourceCodec: "opus", Dest: dest, Format: "mp3",
		Track: spotify.Track{Title: "T", Artists: []string{"A"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ffprobe(t, dest).Streams {
		if s.CodecType == "video" {
			t.Error("unexpected picture stream")
		}
	}
}

func TestEncodeMissingFFmpeg(t *testing.T) {
	err := Encode(context.Background(), "definitely-not-ffmpeg", Job{Dest: filepath.Join(t.TempDir(), "x.opus"), Format: "opus"})
	if err == nil || !strings.Contains(err.Error(), "ffmpeg not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestQualityWarning(t *testing.T) {
	tests := []struct {
		format, bitrate string
		kbps            float64
		codec           string
		wantContains    string // "" means no warning
	}{
		{"opus", "", 152.3, "opus", ""},
		{"mp3", "", 152.3, "opus", ""},
		{"mp3", "128k", 152.3, "opus", ""},
		{"mp3", "160k", 152.3, "opus", ""}, // within 10%: not worth a warning
		{"mp3", "320k", 152.3, "opus", "320k requested but YouTube's source is 152k Opus"},
		{"opus", "256k", 129.5, "mp4a.40.2", "source is 130k AAC"},
		{"flac", "", 152.3, "opus", "FLAC from YouTube's 152k Opus source"},
		{"mp3", "320k", 0, "", ""}, // unknown source: say nothing
	}
	for _, tt := range tests {
		got := QualityWarning(tt.format, tt.bitrate, tt.kbps, tt.codec)
		if (tt.wantContains == "") != (got == "") || !strings.Contains(got, tt.wantContains) {
			t.Errorf("QualityWarning(%s, %q, %v) = %q, want containing %q", tt.format, tt.bitrate, tt.kbps, got, tt.wantContains)
		}
	}
}
