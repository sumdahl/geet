package deezer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/spotify-dl/internal/spotify"
)

// Deezer's canned responses, keyed by path plus decoded q parameter.
var responses = map[string]string{
	"/search/album?q=The Beatles The Beatles (Remastered)": `{"data":[
		{"id":1,"title":"The Beatles 1967 - 1970 (Remastered)","nb_tracks":28,"artist":{"name":"The Beatles"}},
		{"id":2,"title":"The Beatles (Remastered)","nb_tracks":3,"artist":{"name":"The Beatles"}}]}`,
	"/album/2/tracks": `{"data":[
		{"title":"Back In The U.S.S.R. (Remastered 2009)","isrc":"GB1","duration":163,"track_position":1,"disk_number":1},
		{"title":"Dear Prudence (Remastered 2009)","isrc":"GB2","duration":235,"track_position":2,"disk_number":1}],
		"next":"{{base}}/album/2/tracks?index=2"}`,
	"/album/2/tracks?index=2": `{"data":[
		{"title":"Birthday (Remastered 2009)","isrc":"GB3","duration":162,"track_position":1,"disk_number":2}]}`,

	"/search/album?q=Rick Astley Whenever You Need Somebody": `{"data":[
		{"id":3,"title":"Whenever You Need Somebody","nb_tracks":10,"artist":{"name":"Rick Astley"}}]}`,
	"/album/3/tracks": `{"data":[
		{"title":"Never Gonna Give You Up","isrc":"GBARL9300135","duration":213,"track_position":1,"disk_number":1}]}`,

	"/search/album?q=Queen Greatest Hits": `{"data":[]}`,
	"/search?q=Queen Bohemian Rhapsody": `{"data":[
		{"id":10,"title":"Bohemian Rhapsody (Live Aid)","duration":360,"artist":{"name":"Queen"},"album":{"title":"Live"}},
		{"id":11,"title":"Bohemian Rhapsody","duration":355,"artist":{"name":"Piano Guys"},"album":{"title":"Covers"}},
		{"id":12,"title":"Bohemian Rhapsody - Remastered 2011","duration":354,"artist":{"name":"Queen"},"album":{"title":"A Night at the Opera"}}]}`,
	"/track/12": `{"title":"Bohemian Rhapsody","isrc":"GBUM71029604","duration":354,"track_position":11,"disk_number":1}`,

	"/search/album?q=Nobody Unknown": `{"data":[]}`,
	"/search?q=Nobody Obscure":       `{"data":[]}`,
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if q := r.URL.Query().Get("q"); q != "" {
			key += "?q=" + q
		} else if i := r.URL.Query().Get("index"); i != "" {
			key += "?index=" + i
		}
		body, ok := responses[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			body = `{"error":{"type":"DataException","message":"no data","code":800}}`
		}
		w.Write([]byte(strings.ReplaceAll(body, "{{base}}", srv.URL)))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestEnrichAlbum(t *testing.T) {
	c := newTestClient(t)
	// Spotify numbers these 1..3 by position; Deezer puts the third on disc 2.
	tracks := []spotify.Track{
		{Title: "Back In The U.S.S.R. - Remastered 2009", Duration: 163453 * time.Millisecond, TrackNumber: 1},
		{Title: "Dear Prudence - Remastered 2009", Duration: 235 * time.Second, TrackNumber: 2},
		{Title: "Birthday - Remastered 2009", Duration: 162700 * time.Millisecond, TrackNumber: 3},
	}
	for i := range tracks {
		tracks[i].Artists = []string{"The Beatles"}
		tracks[i].AlbumArtist = "The Beatles"
		tracks[i].Album = "The Beatles (Remastered)"
	}
	if err := c.EnrichAlbum(context.Background(), tracks); err != nil {
		t.Fatal(err)
	}
	want := [][3]any{{"GB1", 1, 1}, {"GB2", 1, 2}, {"GB3", 2, 1}}
	for i, tr := range tracks {
		got := [3]any{tr.ISRC, tr.DiscNumber, tr.TrackNumber}
		if got != want[i] {
			t.Errorf("track %d: got (isrc, disc, track) %v, want %v", i, got, want[i])
		}
	}
}

func TestEnrichTrack(t *testing.T) {
	tests := []struct {
		name string
		in   spotify.Track
		want spotify.Track
	}{
		{
			name: "found on its own album: ISRC and disc",
			in:   spotify.Track{Title: "Never Gonna Give You Up", Artists: []string{"Rick Astley"}, AlbumArtist: "Rick Astley", Album: "Whenever You Need Somebody", TrackNumber: 1, Duration: 213573 * time.Millisecond},
			want: spotify.Track{Title: "Never Gonna Give You Up", Artists: []string{"Rick Astley"}, AlbumArtist: "Rick Astley", Album: "Whenever You Need Somebody", TrackNumber: 1, DiscNumber: 1, Duration: 213573 * time.Millisecond, ISRC: "GBARL9300135"},
		},
		{
			name: "search skips live cut and cover, takes ISRC only from another release",
			in:   spotify.Track{Title: "Bohemian Rhapsody", Artists: []string{"Queen"}, AlbumArtist: "Queen", Album: "Greatest Hits", TrackNumber: 1, Duration: 354320 * time.Millisecond},
			want: spotify.Track{Title: "Bohemian Rhapsody", Artists: []string{"Queen"}, AlbumArtist: "Queen", Album: "Greatest Hits", TrackNumber: 1, Duration: 354320 * time.Millisecond, ISRC: "GBUM71029604"},
		},
		{
			name: "no match leaves the track alone",
			in:   spotify.Track{Title: "Obscure", Artists: []string{"Nobody"}, AlbumArtist: "Nobody", Album: "Unknown", Duration: time.Minute},
			want: spotify.Track{Title: "Obscure", Artists: []string{"Nobody"}, AlbumArtist: "Nobody", Album: "Unknown", Duration: time.Minute},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t)
			got := tt.in
			if err := c.EnrichTrack(context.Background(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestSameRecording(t *testing.T) {
	tests := []struct {
		spotify     string
		spotifySecs int
		deezer      string
		deezerSecs  int
		want        bool
	}{
		{"Back In The U.S.S.R. - Remastered 2009", 163, "Back In The U.S.S.R. (Remastered 2009)", 163, true},
		{"Global Warming (feat. Sensato)", 85, "Global Warming", 85, true},
		{"Help!", 139, "Help", 139, true},
		{"Blinding Lights", 200, "Blinding Lights", 203, true},
		{"Blinding Lights", 200, "Blinding Lights", 204, false},
		{"Blinding Lights", 200, "Blinding Lights (Remix)", 216, false},
		{"Yesterday", 125, "Tomorrow", 125, false},
	}
	for _, tt := range tests {
		t.Run(tt.spotify+"|"+tt.deezer, func(t *testing.T) {
			tr := spotify.Track{Title: tt.spotify, Duration: time.Duration(tt.spotifySecs) * time.Second}
			if got := sameRecording(tr, tt.deezer, tt.deezerSecs); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
