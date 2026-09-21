package itunes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// topSongsServer serves Apple's real most-played feed from testdata, plus a
// lookup for each song in it, since the feed carries only names and ids.
func topSongsServer(t *testing.T, lookupFails bool) (*Client, *int) {
	t.Helper()
	lookups := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "most-played"):
			body, err := os.ReadFile(filepath.Join("testdata", "top_songs_us.json"))
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(body)
		case strings.HasSuffix(r.URL.Path, "/lookup"):
			lookups++
			if lookupFails {
				http.Error(w, "nope", http.StatusInternalServerError)
				return
			}
			id := r.URL.Query().Get("id")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resultCount": 1,
				"results": []map[string]any{{
					"kind": "song", "trackId": 123, "trackName": "Song " + id,
					"artistName": "Someone", "collectionName": "An Album",
					"trackTimeMillis": 200000, "trackViewUrl": "https://music.apple.com/x",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "US")
	// Point the feed at the same server.
	old := FeedBaseURL
	FeedBaseURL = srv.URL + "/api/v2"
	t.Cleanup(func() { FeedBaseURL = old })
	return c, &lookups
}

func TestTopSongs(t *testing.T) {
	c, lookups := topSongsServer(t, false)
	tracks, err := c.TopSongs(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 {
		t.Fatalf("got %d songs, want 3", len(tracks))
	}
	if *lookups != 3 {
		t.Errorf("looked songs up %d times, want 3", *lookups)
	}
	for i, tr := range tracks {
		if tr.Title == "" || len(tr.Artists) == 0 || tr.Duration == 0 {
			t.Errorf("song %d is missing details: %+v", i, tr)
		}
	}
}

// A song the store lists but won't look up must not take the chart down
// with it.
func TestTopSongsSurvivesALookupFailure(t *testing.T) {
	c, _ := topSongsServer(t, true)
	tracks, err := c.TopSongs(context.Background(), 3)
	if err != nil {
		t.Fatalf("a failed lookup should not fail the chart: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("got %d songs from failing lookups", len(tracks))
	}
}
