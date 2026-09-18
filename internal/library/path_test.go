package library

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sumdahl/spotify-dl/internal/spotify"
)

func TestPath(t *testing.T) {
	base := spotify.Track{
		ID: "abc", Title: "Ancestral", Artists: []string{"Steven Wilson"}, AlbumArtist: "Steven Wilson",
		Album: "Hand Cannot Erase", TrackNumber: 7, DiscNumber: 1, Year: 2015, ISRC: "GBCQV1400523",
	}
	tests := []struct {
		name  string
		tmpl  string
		track func(*spotify.Track)
		want  string
	}{
		{name: "default", tmpl: DefaultTemplate, want: "/m/Ancestral - Steven Wilson.opus"},
		{
			name:  "default with featured artists",
			tmpl:  DefaultTemplate,
			track: func(t *spotify.Track) { t.Artists = []string{"Pitbull", "Chris Brown"}; t.Title = "Hope We Meet Again" },
			want:  "/m/Hope We Meet Again - Pitbull, Chris Brown.opus",
		},
		{name: "album layout", tmpl: "{album_artist}/{album}/{track} {title}", want: "/m/Steven Wilson/Hand Cannot Erase/07 Ancestral.opus"},
		{name: "flat", tmpl: "{artists} - {title}", want: "/m/Steven Wilson - Ancestral.opus"},
		{name: "all fields", tmpl: "{year}/{disc}-{track} {artist} {isrc} {spotify_id} {title}", want: "/m/2015/1-07 Steven Wilson GBCQV1400523 abc Ancestral.opus"},
		{
			name:  "slashes in values stay in one component",
			tmpl:  "{album_artist}/{album}/{track} {title}",
			track: func(t *spotify.Track) { t.AlbumArtist = "AC/DC"; t.Album = "../../etc"; t.Title = "Why?: <Now>" },
			want:  "/m/AC-DC/-..-etc/07 Why Now.opus",
		},
		{
			name:  "missing values fall back",
			tmpl:  "{album_artist}/{year}/{track} {title}",
			track: func(t *spotify.Track) { t.AlbumArtist = ""; t.Year = 0; t.TrackNumber = 0 },
			want:  "/m/Steven Wilson/Unknown/Ancestral.opus",
		},
		{
			name:  "unknown disc renders as 1",
			tmpl:  "{disc}-{track} {title}",
			track: func(t *spotify.Track) { t.DiscNumber = 0 },
			want:  "/m/1-07 Ancestral.opus",
		},
		{
			name: "nepali title",
			tmpl: "{artists} - {title}",
			track: func(t *spotify.Track) {
				t.Artists = []string{"सज्जन राज वैद्य"}
				t.Title = "हताररिँदै, बतासिँदै"
			},
			want: "/m/सज्जन राज वैद्य - हताररिँदै, बतासिँदै.opus",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := base
			if tt.track != nil {
				tt.track(&tr)
			}
			if err := ValidateTemplate(tt.tmpl); err != nil {
				t.Fatal(err)
			}
			if got := Path("/m", tt.tmpl, tr, "opus"); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestPathTruncatesLongNames(t *testing.T) {
	tr := spotify.Track{Title: strings.Repeat("ह", 150)} // 3 bytes each
	got := Path("/m", "{title}", tr, "flac")
	name := strings.TrimPrefix(got, "/m/")
	if len(name) > maxSegmentBytes || !strings.HasSuffix(name, ".flac") {
		t.Fatalf("name is %d bytes: %q", len(name), name)
	}
}

func TestValidateTemplate(t *testing.T) {
	tests := []struct {
		tmpl string
		ok   bool
	}{
		{DefaultTemplate, true},
		{"{title}", true},
		{"", false},
		{"/abs/{title}", false},
		{"../{title}", false},
		{"{artist}/{album}", false},
		{"{artist}/{name}", false},
		{"{title} {bogus}", false},
	}
	for _, tt := range tests {
		if err := ValidateTemplate(tt.tmpl); (err == nil) != tt.ok {
			t.Errorf("ValidateTemplate(%q) = %v, want ok=%v", tt.tmpl, err, tt.ok)
		}
	}
}

func TestFolderName(t *testing.T) {
	tests := []struct {
		name, letterCase, want string
	}{
		{"Playlist 1", "lower", "playlist-1"},
		{"Road Trip Mix", "lower", "road-trip-mix"},
		{"Road Trip Mix", "capitalize", "Road-trip-mix"},
		{"road trip MIX", "title", "Road-Trip-Mix"},
		{"Spotify's Most Played All-Time [Updated Weekly] | Most Streamed", "lower", "spotifys-most-played-all-time-updated-weekly-most-streamed"},
		{"  chill 🔥 vibes!!  2024  ", "lower", "chill-vibes-2024"},
		{"lo_fi / study.beats", "lower", "lo-fi-study-beats"},
		{"नेपाली गीतहरू", "title", "नेपाली-गीतहरू"},
		{"Beyoncé's Hits", "title", "Beyoncés-Hits"},
		{"🔥🔥🔥", "lower", "playlist"},
		{"", "lower", "playlist"},
	}
	for _, tt := range tests {
		if got := FolderName(tt.name, tt.letterCase); got != tt.want {
			t.Errorf("FolderName(%q, %s) = %q, want %q", tt.name, tt.letterCase, got, tt.want)
		}
	}
}

func TestFolderNameLength(t *testing.T) {
	got := FolderName(strings.Repeat("verylongword ", 30), "lower")
	if len(got) > maxFolderBytes || strings.HasSuffix(got, "-") || strings.Contains(got, " ") {
		t.Errorf("got %d bytes: %q", len(got), got)
	}
}

// Any metadata, however broken, must yield a path inside root: one file,
// no traversal, valid UTF-8, within the length limits.
func FuzzPath(f *testing.F) {
	for _, s := range []string{"AC/DC", "../../etc", "हताररिँदै, बतासिँदै", "🔥🔥🔥", ".", "..", "a\x00b\xff/\\:*?\"<>|", "   "} {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, title, artist string) {
		tr := spotify.Track{Title: title, Artists: []string{artist}, AlbumArtist: artist, Album: title}
		for _, tmpl := range []string{DefaultTemplate, "{album_artist}/{album}/{track} {title}"} {
			p := Path("/root", tmpl, tr, "opus")
			rel, err := filepath.Rel("/root", p)
			if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				t.Fatalf("path %q escapes root", p)
			}
			if got, want := strings.Count(rel, "/"), strings.Count(tmpl, "/"); got != want {
				t.Fatalf("path %q has %d separators, template %d", p, got, want)
			}
			for _, seg := range strings.Split(rel, "/") {
				if len(seg) > maxSegmentBytes || seg == "" || seg == "." || seg == ".." {
					t.Fatalf("bad segment %q in %q", seg, p)
				}
			}
			if !strings.HasSuffix(p, ".opus") {
				t.Fatalf("lost extension: %q", p)
			}
		}
		for _, c := range FolderCases {
			name := FolderName(title, c)
			if name == "" || len(name) > maxFolderBytes || strings.ContainsAny(name, " /\\") || !utf8.ValidString(name) {
				t.Fatalf("FolderName(%q, %s) = %q", title, c, name)
			}
		}
	})
}
