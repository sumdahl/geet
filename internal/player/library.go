package player

import (
	"bufio"
	"context"
	"io/fs"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
)

// Item is one entry in the play queue. It is either a file on disk or a
// song to stream: geet plays what you already have straight from the
// library, and anything else from YouTube while you decide whether to keep
// it.
type Item struct {
	Path  string // empty for a song that is not downloaded
	Track spotify.Track
	// Added is the file's modification time, which is when geet saved it.
	Added time.Time
	// Stream is filled in when the song is played without downloading. It
	// is resolved as the song comes up, not in advance: finding a whole
	// playlist on YouTube before the first note would take a minute.
	Stream Stream
	// Saving marks a streaming song the listener asked geet to keep.
	Saving bool
}

// Downloaded reports whether the song is a file geet can play offline.
func (i Item) Downloaded() bool { return i.Path != "" }

// Source is what to hand the player: the file when there is one, else the
// stream.
func (i Item) Source() string {
	if i.Path != "" {
		return i.Path
	}
	return i.Stream.Direct
}

// Name is what a display calls this item, whether or not tags were read.
func (i Item) Name() string {
	if i.Track.Title != "" {
		if artists := strings.Join(i.Track.Artists, ", "); artists != "" {
			return artists + " — " + i.Track.Title
		}
		return i.Track.Title
	}
	if i.Path == "" {
		return "unknown song"
	}
	return strings.TrimSuffix(filepath.Base(i.Path), filepath.Ext(i.Path))
}

// audioExts are the formats geet writes, plus the ones a library is likely
// to already hold.
var audioExts = map[string]bool{
	".opus": true, ".mp3": true, ".flac": true, ".m4a": true,
	".ogg": true, ".wav": true, ".aac": true, ".oga": true,
}

// IsAudio reports whether a path looks like a song file.
func IsAudio(path string) bool {
	return audioExts[strings.ToLower(filepath.Ext(path))]
}

// Scan lists the audio files under root, newest first. It reads no tags:
// a library of thousands of songs must open instantly, and tags are read
// only for the song about to play.
func Scan(root string) ([]Item, error) {
	var items []Item
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable folder is not worth failing the whole scan
		}
		if d.IsDir() || !IsAudio(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		items = append(items, Item{Path: path, Added: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Added.After(items[j].Added) })
	return items, nil
}

// Match filters a library by words typed at the prompt: every word must
// appear somewhere in the path, so "bartika najeek" finds the song without
// caring about order, case or the folder it sits in.
func Match(items []Item, query string) []Item {
	words := textnorm.Words(query)
	if len(words) == 0 {
		return items
	}
	var out []Item
	for _, it := range items {
		hay := textnorm.Norm(strings.ReplaceAll(it.Path, "/", " "))
		all := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				all = false
				break
			}
		}
		if all {
			out = append(out, it)
		}
	}
	return out
}

// Shuffle reorders items in place.
func Shuffle(items []Item) {
	rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
}

// ReadTags fills in what a file's tags say, for the display and for the
// lyrics lookup. A file without tags is still playable, so every failure
// here is silent.
func ReadTags(ctx context.Context, ffprobe, path string) spotify.Track {
	t := spotify.Track{}
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error",
		"-show_entries", "format=duration:format_tags:stream_tags",
		"-of", "default=noprint_wrappers=1", path).Output()
	if err != nil {
		return t
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64<<10), 32<<20) // an embedded cover arrives as one huge tag
	for sc.Scan() {
		k, v, found := strings.Cut(sc.Text(), "=")
		if !found || v == "" {
			continue
		}
		k = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(k, "TAG:"), "tag:"))
		switch k {
		case "title":
			t.Title = v
		case "artist":
			t.Artists = splitArtists(v)
		case "album_artist", "albumartist":
			t.AlbumArtist = v
		case "album":
			t.Album = v
		case "isrc", "tsrc":
			t.ISRC = v
		case "date", "year":
			if y, err := strconv.Atoi(firstNumber(v)); err == nil {
				t.Year = y
			}
		case "duration":
			if secs, err := strconv.ParseFloat(v, 64); err == nil {
				t.Duration = time.Duration(secs * float64(time.Second))
			}
		}
	}
	if t.Title == "" {
		// Fall back to the file name, which geet writes as
		// "{title} - {artists}".
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if title, artists, ok := strings.Cut(base, " - "); ok {
			t.Title, t.Artists = title, splitArtists(artists)
		} else {
			t.Title = base
		}
	}
	return t
}

func splitArtists(v string) []string {
	var out []string
	for _, part := range strings.Split(strings.NewReplacer(";", ",", "/", ",").Replace(v), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNumber(v string) string {
	for i, r := range v {
		if r < '0' || r > '9' {
			return v[:i]
		}
	}
	return v
}
