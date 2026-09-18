package spotify

import (
	"context"
	"encoding/json"
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

	"golang.org/x/time/rate"
)

// unpaced drops the request pacing, which would only slow the tests down;
// TestWebPacesRequests covers it.
func unpaced(w *Web) *Web {
	w.pace = rate.NewLimiter(rate.Inf, 0)
	return w
}

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
	return unpaced(NewWeb(srv.URL))
}

func TestWebResolve(t *testing.T) {
	w := newTestWeb(t)
	one := Track{ID: "t1", Title: "One", Artists: []string{"Alpha"}, AlbumArtist: "Alpha, Beta", Album: "Split Album",
		CoverURL: "https://img/640", TrackNumber: 1, Year: 2011, Duration: 61 * time.Second, Explicit: true}
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
	// The playlist has 4 items, but the podcast episode isn't read.
	want := [][2]int{{0, 3}, {1, 3}, {2, 3}, {3, 3}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("progress %v, want %v", calls, want)
	}
}

func TestWebPlaylistParallelKeepsOrder(t *testing.T) {
	w := newTestWeb(t)
	w.Workers = 4
	col, err := w.Resolve(context.Background(), Ref{KindPlaylist, "pl"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tr := range col.Tracks {
		ids = append(ids, tr.ID)
	}
	if want := []string{"t2", "tx"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("order %v, want %v", ids, want)
	}
}

func TestWebRetriesRateLimit(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			rw.Header().Set("Retry-After", "1")
			rw.WriteHeader(http.StatusTooManyRequests)
			return
		}
		body, _ := os.ReadFile(filepath.Join("testdata", "embed_album.html"))
		rw.Write(body)
	}))
	defer srv.Close()
	w := unpaced(NewWeb(srv.URL))
	w.backoff = time.Millisecond
	tracks, err := w.Album(context.Background(), "alb")
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 || len(tracks) != 2 {
		t.Errorf("calls=%d tracks=%d", calls, len(tracks))
	}
}

// A playlist whose public page lists the maximum 100 tracks is truncated;
// its real size comes from the page summary ("201 items").
func TestWebPlaylistTruncated(t *testing.T) {
	items := make([]map[string]any, embedPlaylistCap)
	for i := range items {
		items[i] = map[string]any{"uri": "spotify:track:t1", "title": "One", "subtitle": "Alpha", "duration": 61000, "entityType": "track"}
	}
	embed, _ := json.Marshal(map[string]any{"props": map[string]any{"pageProps": map[string]any{"state": map[string]any{"data": map[string]any{
		"entity": map[string]any{"name": "Big Mix", "trackList": items},
	}}}}})
	album, _ := os.ReadFile(filepath.Join("testdata", "embed_album.html"))
	page, _ := os.ReadFile(filepath.Join("testdata", "page_t1.html"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embed/playlist/big":
			w.Write([]byte(`<script id="__NEXT_DATA__" type="application/json">` + string(embed) + `</script>`))
		case "/playlist/big":
			w.Write([]byte(`<meta property="og:title" content="Big Mix"/><meta property="og:description" content="Playlist · Sumiran · 1,201 items"/>`))
		case "/embed/album/alb":
			w.Write(album)
		case "/track/t1":
			w.Write(page)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	w := unpaced(NewWeb(srv.URL))
	w.Workers = 8
	col, err := w.Resolve(context.Background(), Ref{KindPlaylist, "big"})
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Tracks) != embedPlaylistCap || col.Total != 1201 || col.Name != "Big Mix" {
		t.Errorf("got %d tracks, Total %d, name %q; want 100, 1201, Big Mix", len(col.Tracks), col.Total, col.Name)
	}
	if name, err := w.Name(context.Background(), Ref{KindPlaylist, "big"}); err != nil || name != "Big Mix" {
		t.Errorf("Name = %q, %v", name, err)
	}
}

func TestWebPlaylistNotTruncated(t *testing.T) {
	col, err := newTestWeb(t).Resolve(context.Background(), Ref{KindPlaylist, "pl"})
	if err != nil {
		t.Fatal(err)
	}
	if col.Total != 0 {
		t.Errorf("Total = %d for a playlist read in full", col.Total)
	}
}

func TestWebTracksKeepsOrderAndSkipsGone(t *testing.T) {
	w := newTestWeb(t)
	w.Workers = 4
	got, skipped, err := w.Tracks(context.Background(), []string{"tx", "gone", "t2", "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Errorf("skipped %d, want 1 (the gone track)", skipped)
	}
	var ids []string
	for _, tr := range got {
		ids = append(ids, tr.ID)
	}
	if want := []string{"tx", "t2", "t1"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids %v, want %v", ids, want)
	}
}

// Spotify answers 429 to a burst of reads: the workers pause together and
// every track still arrives; a track that stays limited is skipped, not
// fatal.
func TestWebTracksRateLimited(t *testing.T) {
	album, _ := os.ReadFile(filepath.Join("testdata", "embed_album.html"))
	pages := map[string][]byte{}
	for _, id := range []string{"t1", "t2"} {
		pages[id], _ = os.ReadFile(filepath.Join("testdata", "page_"+id+".html"))
	}
	var calls, limited atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/always-limited") || n%3 == 0 { // every third request, and one track forever
			limited.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		switch r.URL.Path {
		case "/embed/album/alb":
			w.Write(album)
		case "/track/t1":
			w.Write(pages["t1"])
		case "/track/t2":
			w.Write(pages["t2"])
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	w := unpaced(NewWeb(srv.URL))
	w.Workers = 4
	w.backoff = time.Millisecond
	got, skipped, err := w.Tracks(context.Background(), []string{"t1", "t2", "always-limited", "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || skipped != 1 {
		t.Errorf("got %d tracks, %d skipped; want 3 and 1 (the permanently limited one)", len(got), skipped)
	}
	if limited.Load() == 0 {
		t.Fatal("the test server never rate-limited")
	}

	// Every track failing is an error, not an empty success.
	_, _, err = w.Tracks(context.Background(), []string{"always-limited"})
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("all limited: err = %v", err)
	}
}
