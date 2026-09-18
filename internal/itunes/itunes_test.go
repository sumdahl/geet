package itunes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
)

// newTestClient serves the real catalog responses captured in testdata,
// keyed by the search term (or "lookup").
func newTestClient(t *testing.T) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.URL.Query().Get("country"); got != "US" {
			t.Errorf("country = %q, want US", got)
		}
		var name string
		switch r.URL.Path {
		case "/search":
			if r.URL.Query().Get("entity") != "song" || r.URL.Query().Get("limit") != "50" {
				t.Errorf("unexpected search params %s", r.URL.RawQuery)
			}
			name = "search_" + strings.ReplaceAll(r.URL.Query().Get("term"), " ", "_") + ".json"
		case "/lookup":
			if r.URL.Query().Get("id") != "1499378607" {
				w.Write([]byte(`{"resultCount":0,"results":[]}`))
				return
			}
			name = "lookup.json"
		}
		body, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "us"), &calls
}

func TestLookupMapsTrack(t *testing.T) {
	c, _ := newTestClient(t)
	got, err := c.Lookup(context.Background(), "1499378607")
	if err != nil {
		t.Fatal(err)
	}
	want := spotify.Track{
		ID: "itunes:1499378607", Title: "Blinding Lights", Artists: []string{"The Weeknd"},
		AlbumArtist: "The Weeknd", Album: "After Hours",
		CoverURL:    "https://is1-ssl.mzstatic.com/image/thumb/Music125/v4/6f/bc/e6/6fbce6c4-c38c-72d8-4fd0-66cfff32f679/20UMGIM12176.rgb.jpg/600x600bb.jpg",
		TrackNumber: 9, DiscNumber: 1, Year: 2019, Duration: 200046 * time.Millisecond,
		SourceURL: got.SourceURL,
	}
	if !strings.Contains(got.SourceURL, "music.apple.com") || !strings.Contains(got.SourceURL, "i=1499378607") {
		t.Errorf("SourceURL = %q", got.SourceURL)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if got.URL() != got.SourceURL {
		t.Errorf("URL() = %q, want the Apple Music link", got.URL())
	}

	if _, err := c.Lookup(context.Background(), "1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v", err)
	}
}

func TestRankRealSearches(t *testing.T) {
	c, _ := newTestClient(t)

	t.Run("original beats covers; editions merged", func(t *testing.T) {
		tracks, err := c.Search(context.Background(), "blinding lights")
		if err != nil {
			t.Fatal(err)
		}
		ranked := Rank("blinding lights", tracks)
		top := ranked[0]
		if top.Artists[0] != "The Weeknd" || top.Title != "Blinding Lights" {
			t.Fatalf("top = %s by %v (%d editions); want The Weeknd's original", top.Title, top.Artists, top.Editions)
		}
		if top.Editions < 4 {
			t.Errorf("original merged %d editions, want the album and its reissues together", top.Editions)
		}
		for i, r := range ranked {
			if r.Title == "Blinding Lights (Remix)" && i == 0 {
				t.Error("remix ranked first")
			}
			// Plain-titled tribute/lullaby versions sink below the covers.
			if (strings.Contains(r.Album, "Lullaby") || strings.Contains(strings.Join(r.Artists, " "), "Tribute")) && i < 5 {
				t.Errorf("%s by %v · %s ranked %d", r.Title, r.Artists, r.Album, i+1)
			}
		}
		if len(ranked) >= len(tracks) {
			t.Errorf("%d results from %d tracks: nothing merged", len(ranked), len(tracks))
		}
	})

	t.Run("instrumental and slowed versions sink", func(t *testing.T) {
		tracks, _ := c.Search(context.Background(), "fukumean")
		ranked := Rank("fukumean", tracks)
		for _, r := range ranked[:3] {
			low := strings.ToLower(r.Title)
			if strings.Contains(low, "instrumental") || strings.Contains(low, "slowed") || strings.Contains(low, "karaoke") {
				t.Errorf("variant %q in the top 3", r.Title)
			}
		}
	})

	t.Run("nepali artist search", func(t *testing.T) {
		tracks, _ := c.Search(context.Background(), "sajjan raj vaidya")
		ranked := Rank("sajjan raj vaidya", tracks)
		for _, r := range ranked[:5] {
			if r.Artists[0] != "Sajjan Raj Vaidya" {
				t.Errorf("top 5 includes %s by %v", r.Title, r.Artists)
			}
		}
	})
}

func TestRankCleanEditAfterExplicit(t *testing.T) {
	explicit := spotify.Track{Title: "Song", Artists: []string{"A"}, Duration: 100 * time.Second}
	clean := explicit
	clean.Clean = true
	clean.Duration = 130 * time.Second // a different recording, so not merged
	ranked := Rank("song", []spotify.Track{clean, explicit})
	if ranked[0].Clean {
		t.Error("clean edit ranked above the explicit original")
	}
}

func TestSplitArtists(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"The Weeknd", []string{"The Weeknd"}},
		{"The Weeknd & ROSALÍA", []string{"The Weeknd", "ROSALÍA"}},
		{"Metro Boomin, The Weeknd & 21 Savage", []string{"Metro Boomin", "The Weeknd", "21 Savage"}},
		{"Gunna feat. Young Thug", []string{"Gunna", "Young Thug"}},
		{"X Ambassadors", []string{"X Ambassadors"}},
		{"Lil Nas X", []string{"Lil Nas X"}},
		{"सज्जन राज वैद्य", []string{"सज्जन राज वैद्य"}},
	}
	for _, tt := range tests {
		if got := splitArtists(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitArtists(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRetriesRateLimit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		body, _ := os.ReadFile(filepath.Join("testdata", "lookup.json"))
		w.Write(body)
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "US").Lookup(context.Background(), "1499378607"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("%d calls, want a retry", calls.Load())
	}
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"itunes:1499378607", "1499378607"},
		{"https://music.apple.com/us/album/blinding-lights/1499378108?i=1499378607&uo=4", "1499378607"},
		{"https://music.apple.com/np/song/blinding-lights/1499378607", "1499378607"},
		{"https://music.apple.com/us/song/1499378607", "1499378607"},
		{"  itunes:42\n", "42"},
		{"itunes:", ""},
		{"itunes:abc", ""},
		{"https://music.apple.com/us/album/after-hours/1499378108", ""},
		{"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC", ""},
		{"https://evil.example/us/song/x/1", ""},
	}
	for _, tt := range tests {
		got, err := ParseRef(tt.in)
		if (err != nil) != (tt.want == "") || got != tt.want {
			t.Errorf("ParseRef(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
		if tt.want != "" && !IsRef(tt.in) {
			t.Errorf("IsRef(%q) = false", tt.in)
		}
	}
	if IsRef("https://open.spotify.com/track/x") {
		t.Error("IsRef accepted a Spotify link")
	}
}

func FuzzParseRef(f *testing.F) {
	for _, s := range []string{"itunes:1", "https://music.apple.com/us/album/x/1?i=2", "https://music.apple.com/us/song/x/3", "%zz", "itunes:\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		id, err := ParseRef(s)
		if err == nil && !isDigits(id) {
			t.Fatalf("ParseRef(%q) = %q", s, id)
		}
	})
}

// The clean edits of explicit songs, in real catalog searches: Enrique
// Iglesias's "Tonight (I'm Fuckin' You)" is "Tonight (I'm Lovin' You)", and
// Gunna's "fukumean" is "umean". "I Like It", by the same artist and
// equally long, must not be taken for a clean edit.
func TestCleanEdit(t *testing.T) {
	c, _ := newTestClient(t)
	tests := []struct {
		name      string
		track     spotify.Track
		term      string
		wantTitle string
	}{
		{
			name:      "words swapped",
			track:     spotify.Track{Title: "Tonight (I'm Fuckin' You)", Artists: []string{"Enrique Iglesias", "Ludacris", "DJ Frank E"}, Duration: 232213 * time.Millisecond, Explicit: true},
			term:      "Enrique Iglesias Tonight",
			wantTitle: "Tonight (I'm Lovin' You) [feat. Ludacris & DJ Frank E]",
		},
		{
			name:      "words cut off",
			track:     spotify.Track{Title: "fukumean", Artists: []string{"Gunna"}, Duration: 125 * time.Second, Explicit: true},
			term:      "Gunna fukumean",
			wantTitle: "umean",
		},
		{
			name:  "no clean edit listed",
			track: spotify.Track{Title: "Some Explicit Song", Artists: []string{"Enrique Iglesias"}, Duration: 232 * time.Second, Explicit: true},
			term:  "Enrique Iglesias Tonight",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanEditTerm(tt.track); tt.name != "no clean edit listed" && got != tt.term {
				t.Errorf("CleanEditTerm = %q, want %q", got, tt.term)
			}
			results, err := c.Search(context.Background(), tt.term)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := CleanEdit(tt.track, results)
			if tt.wantTitle == "" {
				if ok {
					t.Errorf("found %q, want none", got.Title)
				}
				return
			}
			if !ok || got.Title != tt.wantTitle || !got.Clean {
				t.Errorf("CleanEdit = %q (ok %v, clean %v), want %q", got.Title, ok, got.Clean, tt.wantTitle)
			}
		})
	}
}
