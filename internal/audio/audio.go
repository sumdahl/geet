// Package audio turns a downloaded YouTube stream into the final library
// file: it converts (or copies) the audio, writes the tags and embeds the
// cover, all in a single ffmpeg run.
package audio

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // cover decoding for METADATA_BLOCK_PICTURE dimensions
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
)

var ErrToolMissing = errors.New("ffmpeg not found")

var coverClient = &http.Client{Timeout: 30 * time.Second}

type Job struct {
	Source      string // downloaded stream
	SourceCodec string // as yt-dlp reported it
	Dest        string // final file; its extension picks the container
	Format      string // opus, flac or mp3
	Bitrate     string // e.g. "192k"; empty for the format's best
	Track       spotify.Track
	Cover       []byte // may be nil
}

// Encode writes job.Dest directly; callers that need the final file to
// appear atomically encode to a temporary path and rename it.
func Encode(ctx context.Context, ffmpeg string, job Job) error {
	dir := filepath.Dir(job.Dest)
	meta := filepath.Join(dir, "metadata.txt")
	if err := os.WriteFile(meta, ffmetadata(job), 0o600); err != nil {
		return err
	}
	defer os.Remove(meta)

	args := []string{"-y", "-v", "error", "-nostdin", "-i", job.Source, "-i", meta}
	withCover := len(job.Cover) > 0 && job.Format != "opus"
	if withCover {
		cover := filepath.Join(dir, "cover"+coverExt(job.Cover))
		if err := os.WriteFile(cover, job.Cover, 0o600); err != nil {
			return err
		}
		defer os.Remove(cover)
		args = append(args, "-i", cover)
	}

	args = append(args, "-map", "0:a:0", "-map_metadata", "1")
	if job.Format == "opus" {
		// Ogg keeps Opus tags on the stream, not the container, and has no
		// picture stream: the cover travels as a METADATA_BLOCK_PICTURE tag
		// (see ffmetadata).
		args = append(args, "-map_metadata:s:a", "1")
	} else {
		args = append(args, "-map_metadata:s:a", "-1")
	}
	args = append(args, codecArgs(job)...)
	if withCover {
		args = append(args, "-map", "2:v", "-c:v", "copy", "-disposition:v", "attached_pic",
			"-metadata:s:v", "title=Album cover", "-metadata:s:v", "comment=Cover (front)")
	}
	if job.Format == "mp3" {
		// v2.3 is what most players and car stereos actually read.
		args = append(args, "-id3v2_version", "3", "-write_id3v1", "0")
	}
	args = append(args, job.Dest)

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%w: %q (install ffmpeg or set tools.ffmpeg)", ErrToolMissing, ffmpeg)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func codecArgs(job Job) []string {
	switch job.Format {
	case "opus":
		if job.Bitrate == "" && job.SourceCodec == "opus" {
			return []string{"-c:a", "copy"}
		}
		return []string{"-c:a", "libopus", "-b:a", cmp.Or(job.Bitrate, "160k")}
	case "mp3":
		if job.Bitrate == "" {
			return []string{"-c:a", "libmp3lame", "-q:a", "0"} // VBR V0, ~245 kbps
		}
		return []string{"-c:a", "libmp3lame", "-b:a", job.Bitrate}
	default: // flac
		return []string{"-c:a", "flac"}
	}
}

// QualityWarning explains when the requested output can't be better than
// YouTube's source, so a bigger file isn't mistaken for better audio.
func QualityWarning(format, bitrate string, sourceKbps float64, sourceCodec string) string {
	if sourceKbps <= 0 {
		return ""
	}
	src := fmt.Sprintf("%.0fk %s", sourceKbps, codecName(sourceCodec))
	if format == "flac" {
		return fmt.Sprintf("FLAC from YouTube's %s source is lossless packaging of lossy audio: bigger, not better", src)
	}
	if want := kbps(bitrate); want > sourceKbps*1.1 {
		return fmt.Sprintf("%s requested but YouTube's source is %s: the file will be bigger, not better", bitrate, src)
	}
	return ""
}

func kbps(bitrate string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSuffix(bitrate, "k"), 64)
	return v
}

func codecName(c string) string {
	switch {
	case c == "opus":
		return "Opus"
	case strings.HasPrefix(c, "mp4a"):
		return "AAC"
	case c == "":
		return "audio"
	}
	return c
}

// ffmetadata renders the tags in ffmpeg's FFMETADATA1 file format. A file
// rather than -metadata flags, because an opus cover is a ~150 KB base64
// value and Linux caps a single argument at 128 KB.
func ffmetadata(job Job) []byte {
	t := job.Track
	isrcKey := "ISRC"
	if job.Format == "mp3" {
		isrcKey = "TSRC" // ffmpeg writes 4-letter keys as that ID3 frame
	}
	var b bytes.Buffer
	b.WriteString(";FFMETADATA1\n")
	put := func(k, v string) {
		if v != "" {
			b.WriteString(k + "=" + escapeMeta(v) + "\n")
		}
	}
	put("title", t.Title)
	put("artist", strings.Join(t.Artists, ", "))
	put("album_artist", t.AlbumArtist)
	put("album", t.Album)
	if t.TrackNumber > 0 {
		put("track", strconv.Itoa(t.TrackNumber))
	}
	if t.DiscNumber > 0 {
		put("disc", strconv.Itoa(t.DiscNumber))
	}
	if t.Year > 0 {
		put("date", strconv.Itoa(t.Year))
	}
	put(isrcKey, t.ISRC)
	if t.ID != "" {
		put("comment", t.URL())
	}
	if job.Format == "opus" && len(job.Cover) > 0 {
		put("METADATA_BLOCK_PICTURE", pictureBlock(job.Cover))
	}
	return b.Bytes()
}

func escapeMeta(v string) string {
	return strings.NewReplacer(`\`, `\\`, "=", `\=`, ";", `\;`, "#", `\#`, "\n", "\\\n").Replace(v)
}

// pictureBlock is the base64 FLAC PICTURE block that Vorbis-comment formats
// use for cover art.
func pictureBlock(img []byte) string {
	var w, h int
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(img)); err == nil {
		w, h = cfg.Width, cfg.Height
	}
	mime := http.DetectContentType(img)
	var b bytes.Buffer
	be := func(v int) { binary.Write(&b, binary.BigEndian, uint32(v)) }
	be(3) // front cover
	be(len(mime))
	b.WriteString(mime)
	be(0) // no description
	be(w)
	be(h)
	be(24) // colour depth
	be(0)  // not indexed
	be(len(img))
	b.Write(img)
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func coverExt(img []byte) string {
	if http.DetectContentType(img) == "image/png" {
		return ".png"
	}
	return ".jpg"
}

// FetchCover downloads cover art; callers treat failure as "no cover".
func FetchCover(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := coverClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover %s: HTTP %d", url, resp.StatusCode)
	}
	var b bytes.Buffer
	if _, err := b.ReadFrom(http.MaxBytesReader(nil, resp.Body, 10<<20)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
