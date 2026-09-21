package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
)

func track(title, artist string) spotify.Track {
	return spotify.Track{ID: "deezer:" + title, Title: title, Artists: []string{artist}, Duration: 3 * time.Minute}
}

// Two charts of the same week overlap heavily; the same song must not be
// listed twice because two services both have it.
func TestAppendNewSkipsDuplicates(t *testing.T) {
	have := []spotify.Track{track("Dracula", "Tame Impala"), track("Be Her", "Ella Langley")}
	more := []spotify.Track{
		track("dracula", "tame impala"), // same song, different case
		track("Golden", "HUNTR/X"),
	}
	got := appendNew(have, more, 10)
	if len(got) != 3 {
		t.Fatalf("got %d songs, want 3: %+v", len(got), got)
	}
	if got[2].Title != "Golden" {
		t.Errorf("the new song is missing: %+v", got)
	}
}

func TestAppendNewRespectsTheLimit(t *testing.T) {
	have := []spotify.Track{track("One", "A"), track("Two", "B")}
	more := []spotify.Track{track("Three", "C"), track("Four", "D")}
	if got := appendNew(have, more, 3); len(got) != 3 {
		t.Errorf("got %d songs, want 3", len(got))
	}
}

func TestTrendingCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trending.json")
	doc := trendingDoc{
		Source:    "Deezer",
		FetchedAt: time.Now(),
		Tracks:    []spotify.Track{track("Dracula", "Tame Impala"), track("Be Her", "Ella Langley")},
	}
	writeTrendingCache(path, doc)

	got, ok := readTrendingCache(path, time.Hour, 25)
	if !ok {
		t.Fatal("the chart was not read back")
	}
	if len(got.Tracks) != 2 || got.Source != "Deezer" {
		t.Fatalf("got %+v", got)
	}
	// The rows a front end draws are rebuilt from the cache, not stored.
	if len(got.Results) != 2 || got.Results[0].Rank != 1 || got.Results[0].Label == "" {
		t.Errorf("rows were not rebuilt: %+v", got.Results)
	}
	if got.Results[0].Ref != doc.Tracks[0].ID {
		t.Errorf("ref changed: %q", got.Results[0].Ref)
	}

	// A shorter list is served from the same cache.
	if short, _ := readTrendingCache(path, time.Hour, 1); len(short.Tracks) != 1 {
		t.Errorf("limit ignored: %d songs", len(short.Tracks))
	}
}

func TestTrendingCacheExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trending.json")
	writeTrendingCache(path, trendingDoc{
		Source:    "Deezer",
		FetchedAt: time.Now().Add(-8 * time.Hour),
		Tracks:    []spotify.Track{track("Old News", "Someone")},
	})
	if _, ok := readTrendingCache(path, 6*time.Hour, 25); ok {
		t.Error("an 8-hour-old chart was served as fresh")
	}
	// The stale copy is still there for when the network fails.
	if _, ok := readTrendingCache(path, 7*24*time.Hour, 25); !ok {
		t.Error("the stale chart should still be readable as a fallback")
	}
}

func TestTrendingCacheIgnoresRubbish(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"empty.json", ""},
		{"half.json", `{"source":"Deezer","tracks":[`},
		{"no-tracks.json", `{"source":"Deezer","fetched_at":"2099-01-01T00:00:00Z","tracks":[]}`},
	} {
		path := filepath.Join(dir, tc.name)
		if err := writeFile(path, tc.body); err != nil {
			t.Fatal(err)
		}
		if _, ok := readTrendingCache(path, time.Hour, 25); ok {
			t.Errorf("%s was accepted", tc.name)
		}
	}
	if _, ok := readTrendingCache(filepath.Join(dir, "missing.json"), time.Hour, 25); ok {
		t.Error("a missing cache was accepted")
	}
}

// writeFile is a test helper for the rubbish-cache cases.
func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}

func TestAgoLabel(t *testing.T) {
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{time.Second, ", just now"},
		{20 * time.Minute, ", 20 minutes ago"},
		{90 * time.Minute, ", an hour ago"},
		{5 * time.Hour, ", 5 hours ago"},
	} {
		if got := agoLabel(time.Now().Add(-tc.age)); got != tc.want {
			t.Errorf("age %s = %q, want %q", tc.age, got, tc.want)
		}
	}
	if got := agoLabel(time.Time{}); got != "" {
		t.Errorf("an unknown time should say nothing, got %q", got)
	}
}
