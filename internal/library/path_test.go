package library

import (
	"strings"
	"testing"

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
