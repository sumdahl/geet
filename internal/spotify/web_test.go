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

func newTestWeb(t *testing.T) *Web {
	t.Helper()
	routes := map[string]string{
		"/embed/album/alb":     "embed_album.html",
		"/embed/album/alb2":    "embed_album2.html",
		"/embed/playlist/pl":   "embed_playlist.html",
		"/embed/playlist/void": "embed_empty.html",
		"/track/t1":            "page_t1.html",
		"/track/t2":            "page_t2.html",
		"/track/tx":            "page_tx.html",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Spotify only renders the music:* tags for crawlers; mirror that so
		// a wrong User-Agent fails the test instead of passing silently.
		if strings.HasPrefix(r.URL.Path, "/track/") && r.UserAgent() != crawlerUA {
			w.Write([]byte("<html></html>"))
			return
		}
		name, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if name == "embed_empty.html" {
			w.Write([]byte(`<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"state":{"data":{}}}}}</script>`))
			return
		}
		body, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewWeb(srv.URL)
}

func TestWebResolve(t *testing.T) {
	w := newTestWeb(t)
	one := Track{ID: "t1", Title: "One", Artists: []string{"Alpha"}, AlbumArtist: "Alpha, Beta", Album: "Split Album",
		CoverURL: "https://img/640", TrackNumber: 1, Year: 2011, Duration: 61 * time.Second}
	two := Track{ID: "t2", Title: "Two", Artists: []string{"Beta", "Tyler, The Creator"}, AlbumArtist: "Alpha, Beta", Album: "Split Album",
		CoverURL: "https://img/640", TrackNumber: 2, Year: 2011, Duration: 122500 * time.Millisecond}
	loner := Track{ID: "tx", Title: "Loner & Friends", Artists: []string{"Gamma", "Delta"}, AlbumArtist: "Gamma", Album: "Other Album",
		CoverURL: "https://img/g", TrackNumber: 7, Year: 1999, Duration: 200 * time.Second}

	tests := []struct {
		name     string
		ref      Ref
		wantName string
		want     []Track
	}{
		{"track joins page meta with album listing", Ref{KindTrack, "t2"}, "Two", []Track{two}},
		{"track missing from album listing falls back to meta tags", Ref{KindTrack, "tx"}, "Loner & Friends", []Track{loner}},
		{"album numbers by position and takes year from a track page", Ref{KindAlbum, "alb"}, "Split Album", []Track{one, two}},
		{"playlist skips episodes and removed tracks", Ref{KindPlaylist, "pl"}, "Mix", []Track{two, loner}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := w.Resolve(context.Background(), tt.ref)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tt.wantName {
				t.Errorf("name %q, want %q", got.Name, tt.wantName)
			}
			if !reflect.DeepEqual(got.Tracks, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got.Tracks, tt.want)
			}
		})
	}
}

func TestWebResolveErrors(t *testing.T) {
	w := newTestWeb(t)
	tests := []struct {
		name string
		ref  Ref
		want error
	}{
		{"unknown track", Ref{KindTrack, "nope"}, ErrNotFound},
		{"empty embed entity", Ref{KindPlaylist, "void"}, ErrNotFound},
		{"unknown album", Ref{KindAlbum, "nope"}, ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := w.Resolve(context.Background(), tt.ref)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestWebPlaylistProgress(t *testing.T) {
	w := newTestWeb(t)
	var calls [][2]int
	w.OnProgress = func(done, total int) { calls = append(calls, [2]int{done, total}) }
	if _, err := w.Resolve(context.Background(), Ref{KindPlaylist, "pl"}); err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{0, 4}, {1, 4}, {2, 4}, {3, 4}, {4, 4}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("progress %v, want %v", calls, want)
	}
}
