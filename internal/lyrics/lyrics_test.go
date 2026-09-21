package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
)

func TestParseLRCSynced(t *testing.T) {
	const raw = `[ar:Bartika Eam Rai]
[ti:Najeek]
[00:12.50]first line
[00:18.00]second line
[01:05.25][02:10.00]the chorus
`
	got := ParseLRC(raw)
	if !got.Synced {
		t.Fatal("timestamps present but lyrics not marked synced")
	}
	want := []Line{
		{At: 12500 * time.Millisecond, Text: "first line"},
		{At: 18 * time.Second, Text: "second line"},
		{At: 65250 * time.Millisecond, Text: "the chorus"},
		{At: 130 * time.Second, Text: "the chorus"},
	}
	if len(got.Lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got.Lines), len(want), got.Lines)
	}
	for i := range want {
		if got.Lines[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got.Lines[i], want[i])
		}
	}
}

func TestParseLRCPlain(t *testing.T) {
	got := ParseLRC("first line\n\nsecond line\n")
	if got.Synced {
		t.Error("lyrics without timestamps must not be marked synced")
	}
	if len(got.Lines) != 2 || got.Lines[0].Text != "first line" {
		t.Fatalf("got %+v", got.Lines)
	}
	if got.At(30*time.Second) != -1 {
		t.Error("plain lyrics have no current line")
	}
}

func TestParseLRCEmpty(t *testing.T) {
	for _, in := range []string{"", "\n\n", "[ar:Someone]\n[length:03:20]\n"} {
		if got := ParseLRC(in); !got.Empty() {
			t.Errorf("ParseLRC(%q) = %+v, want empty", in, got.Lines)
		}
	}
}

func TestLyricsAt(t *testing.T) {
	l := ParseLRC("[00:10.00]one\n[00:20.00]two\n[00:30.00]three\n")
	for _, tc := range []struct {
		at   time.Duration
		want int
	}{
		{0, -1},
		{9 * time.Second, -1},
		{10 * time.Second, 0},
		{19 * time.Second, 0},
		{25 * time.Second, 1},
		{999 * time.Second, 2},
	} {
		if got := l.At(tc.at); got != tc.want {
			t.Errorf("At(%s) = %d, want %d", tc.at, got, tc.want)
		}
	}
}

func fakeLRCLIB(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL, CacheDir: t.TempDir()}
}

var najeek = spotify.Track{
	ID: "abc", ISRC: "NPX123456789", Title: "Najeek",
	Artists: []string{"Bartika Eam Rai"}, Album: "Bimbaakash", Duration: 312 * time.Second,
}

func TestFetchSynced(t *testing.T) {
	var hits int
	c := fakeLRCLIB(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/get" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("duration"); got != "312" {
			t.Errorf("duration = %q, want 312", got)
		}
		_ = json.NewEncoder(w).Encode(lrclibRecord{
			TrackName: "Najeek", ArtistName: "Bartika Eam Rai", Duration: 312,
			SyncedLyrics: "[00:05.00]hello\n",
		})
	})
	got, err := c.Fetch(context.Background(), najeek)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Synced || len(got.Lines) != 1 {
		t.Fatalf("got %+v", got)
	}
	// The second call must come from the cache.
	if _, err := c.Fetch(context.Background(), najeek); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("asked LRCLIB %d times, want 1", hits)
	}
}

// A song nobody has lyrics for is an everyday outcome: it must come back as
// ErrNotFound, and must not be asked for again.
func TestFetchNotFoundIsCached(t *testing.T) {
	var hits int
	c := fakeLRCLIB(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "not found", http.StatusNotFound)
	})
	for range 3 {
		_, err := c.Fetch(context.Background(), najeek)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if hits > 2 { // one /api/get plus one /api/search
		t.Errorf("a missing song was looked up %d times", hits)
	}
}

// An instrumental has a record but no words; that is also "no lyrics".
func TestFetchInstrumental(t *testing.T) {
	c := fakeLRCLIB(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_ = json.NewEncoder(w).Encode(lrclibRecord{TrackName: "Najeek", Instrumental: true})
	})
	if _, err := c.Fetch(context.Background(), najeek); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A network failure must never be remembered as "this song has no lyrics".
func TestFetchNetworkErrorNotCached(t *testing.T) {
	var hits int
	c := fakeLRCLIB(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	for range 2 {
		if _, err := c.Fetch(context.Background(), najeek); err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want a real error", err)
		}
	}
	if hits < 2 {
		t.Error("a failed lookup was cached as a miss")
	}
}

// The search fallback must not accept another artist's song with the same
// title.
func TestSearchRejectsWrongRecording(t *testing.T) {
	c := fakeLRCLIB(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode([]lrclibRecord{
			{TrackName: "Najeek", ArtistName: "Someone Else", Duration: 312, PlainLyrics: "wrong words"},
			{TrackName: "Najeek", ArtistName: "Bartika Eam Rai", Duration: 600, PlainLyrics: "wrong length"},
			{TrackName: "Najeek", ArtistName: "Bartika Eam Rai", Duration: 313, PlainLyrics: "right one"},
		})
	})
	got, err := c.Fetch(context.Background(), najeek)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 1 || got.Lines[0].Text != "right one" {
		t.Fatalf("got %+v", got.Lines)
	}
}
