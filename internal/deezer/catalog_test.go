package deezer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
)

// newCatalogClient serves real Deezer responses from testdata.
func newCatalogClient(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var name string
		switch {
		case r.URL.Path == "/search" && r.URL.Query().Get("q") == "enrique iglesias tonight":
			name = "search_enrique_iglesias_tonight.json"
		case r.URL.Path == "/chart/0/tracks":
			name = "chart_tracks.json"
		case r.URL.Path == "/track/10202476":
			name = "track_10202476.json"
		default:
			w.Write([]byte(`{"error":{"type":"DataException","message":"no data","code":800}}`))
			return
		}
		body, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// The Apple catalog lacks Enrique Iglesias's explicit "Tonight (I'm Fuckin'
// You)"; Deezer lists it first, marked explicit, and marks a clean cover
// as edited.
func TestCatalogSearch(t *testing.T) {
	tracks, err := newCatalogClient(t).Search(context.Background(), "enrique iglesias tonight")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 25 {
		t.Fatalf("%d tracks, want 25", len(tracks))
	}
	first := tracks[0]
	if first.ID != "deezer:10202476" || first.Title != "Tonight (I'm Fuckin' You)" || !first.Explicit || first.Clean ||
		first.Duration != 234*time.Second || first.SourceURL != "https://www.deezer.com/track/10202476" {
		t.Errorf("first result %+v", first)
	}
	var edited int
	for _, tr := range tracks {
		if tr.Clean {
			edited++
			if tr.Explicit {
				t.Errorf("%q is both clean and explicit", tr.Title)
			}
		}
	}
	if edited == 0 {
		t.Error("no clean edit marked (explicit_content_lyrics 3)")
	}
}

func TestCatalogLookup(t *testing.T) {
	c := newCatalogClient(t)
	got, err := c.Lookup(context.Background(), "10202476")
	if err != nil {
		t.Fatal(err)
	}
	want := spotify.Track{
		ID: "deezer:10202476", Title: "Tonight (I'm Fuckin' You)",
		Artists:     []string{"Enrique Iglesias", "Ludacris", "DJ Frank E"},
		AlbumArtist: "Enrique Iglesias", Album: "Euphoria",
		CoverURL:    "https://cdn-images.dzcdn.net/images/cover/335464b3bab374dbc53bfe2a05f22a8f/1000x1000-000000-80-0-0.jpg",
		TrackNumber: 1, DiscNumber: 1, Year: 2011, Duration: 234 * time.Second, ISRC: "GBUM71029652",
		SourceURL: "https://www.deezer.com/track/10202476", Explicit: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if _, err := c.Lookup(context.Background(), "1"); err == nil {
		t.Error("unknown track: no error")
	}
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"deezer:10202476", "10202476"},
		{"https://www.deezer.com/track/10202476", "10202476"},
		{"https://www.deezer.com/en/track/10202476?utm_source=share", "10202476"},
		{"https://deezer.com/track/10202476/", "10202476"},
		{"deezer:abc", ""},
		{"https://www.deezer.com/album/302127", ""},
		{"https://example.com/track/1", ""},
	}
	for _, tt := range tests {
		got, err := ParseRef(tt.in)
		if tt.want == "" {
			if !errors.Is(err, ErrBadRef) {
				t.Errorf("ParseRef(%q) = %q, %v; want ErrBadRef", tt.in, got, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ParseRef(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
		if !IsRef(tt.in) {
			t.Errorf("IsRef(%q) = false", tt.in)
		}
	}
}

// The chart is what the trending list is built from: real songs with the
// artist, album art and a ref geet can play or download.
func TestChart(t *testing.T) {
	c := newCatalogClient(t)
	tracks, err := c.Chart(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) == 0 {
		t.Fatal("the chart came back empty")
	}
	for i, tr := range tracks {
		if tr.Title == "" || len(tr.Artists) == 0 {
			t.Errorf("track %d has no title or artist: %+v", i, tr)
		}
		if !strings.HasPrefix(tr.ID, RefPrefix) {
			t.Errorf("track %d ref %q is not playable by geet", i, tr.ID)
		}
		if tr.CoverURL == "" {
			t.Errorf("track %d has no cover, which the panel needs", i)
		}
		if tr.Duration <= 0 {
			t.Errorf("track %d has no length", i)
		}
	}
}

// A silly limit must not reach the API as-is.
func TestChartClampsLimit(t *testing.T) {
	c := newCatalogClient(t)
	if _, err := c.Chart(context.Background(), 0); err != nil {
		t.Errorf("limit 0: %v", err)
	}
	if _, err := c.Chart(context.Background(), 5000); err != nil {
		t.Errorf("limit 5000: %v", err)
	}
}
