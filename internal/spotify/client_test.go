package spotify

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
)

// newTestClient serves fixtures keyed by "path?query"; a fixture's {{base}}
// placeholder is replaced with the server URL so `next` links page back to it.
func newTestClient(t *testing.T, routes map[string]string) *Client {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600}`))
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("%s: Authorization = %q", r.URL, got)
		}
		key := strings.TrimPrefix(r.URL.Path, "/v1")
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		name, ok := routes[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"status":404,"message":"Resource not found"}}`))
			return
		}
		body, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(strings.ReplaceAll(string(body), "{{base}}", srv.URL+"/v1")))
	}))
	t.Cleanup(srv.Close)
	return New("id", "secret", WithBaseURL(srv.URL+"/v1"), WithTokenURL(srv.URL+"/token"))
}

func TestResolve(t *testing.T) {
	routes := map[string]string{
		"/tracks/5ghIJDpPoe3CfHMGu71E6T":                        "track.json",
		"/albums/alb":                                           "album.json",
		"/albums/alb/tracks?offset=2&limit=2":                   "album_tracks_2.json",
		"/tracks?ids=t1%2Ct2%2Ct3":                              "tracks_batch.json",
		"/playlists/pl/tracks?limit=100&additional_types=track": "playlist_1.json",
		"/playlists/pl/tracks?offset=3&limit=100":               "playlist_2.json",
	}
	c := newTestClient(t, routes)

	tests := []struct {
		name string
		ref  Ref
		want []Track
	}{
		{
			name: "track picks largest cover and parses year",
			ref:  Ref{KindTrack, "5ghIJDpPoe3CfHMGu71E6T"},
			want: []Track{{
				ID: "5ghIJDpPoe3CfHMGu71E6T", Title: "Bohemian Rhapsody", Artists: []string{"Queen"},
				AlbumArtist: "Queen", Album: "A Night at the Opera", CoverURL: "https://i.scdn.co/image/640",
				TrackNumber: 11, DiscNumber: 1, Year: 1975, Duration: 354320 * time.Millisecond, ISRC: "GBUM71029604",
			}},
		},
		{
			name: "album pages tracks and fills ISRC from batch lookup",
			ref:  Ref{KindAlbum, "alb"},
			want: []Track{
				{ID: "t1", Title: "One", Artists: []string{"Alpha"}, AlbumArtist: "Alpha, Beta", Album: "Split Album",
					CoverURL: "https://img/a640", TrackNumber: 1, DiscNumber: 1, Year: 2011, Duration: time.Second, ISRC: "ISRC0001"},
				{ID: "t2", Title: "Two", Artists: []string{"Beta", "Gamma"}, AlbumArtist: "Alpha, Beta", Album: "Split Album",
					CoverURL: "https://img/a640", TrackNumber: 2, DiscNumber: 1, Year: 2011, Duration: 2 * time.Second, ISRC: "ISRC0002"},
				{ID: "t3", Title: "Three", Artists: []string{"Alpha"}, AlbumArtist: "Alpha, Beta", Album: "Split Album",
					CoverURL: "https://img/a640", TrackNumber: 1, DiscNumber: 2, Year: 2011, Duration: 3 * time.Second},
			},
		},
		{
			name: "playlist pages and skips null, episode and local items",
			ref:  Ref{KindPlaylist, "pl"},
			want: []Track{
				{ID: "p1", Title: "First", Artists: []string{"A"}, AlbumArtist: "A", Album: "AlbA",
					TrackNumber: 3, DiscNumber: 1, Year: 2020, Duration: 3 * time.Minute, ISRC: "PISRC1"},
				{ID: "p2", Title: "Second", Artists: []string{"B"}, AlbumArtist: "B", Album: "AlbB", CoverURL: "https://img/b",
					TrackNumber: 7, DiscNumber: 1, Year: 1999, Duration: 200 * time.Second, ISRC: "PISRC2"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Resolve(context.Background(), tt.ref)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestResolveNotFound(t *testing.T) {
	c := newTestClient(t, nil)
	_, err := c.Resolve(context.Background(), Ref{KindTrack, "missing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
